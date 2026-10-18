package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// A reviewed preview is the only input of an apply. The document reports one
// outcome per selected plugin so the caller can tell an executed native install
// from a manual step or from an installation that must stay pending.
type pluginApplyDoc struct {
	Target      string              `json:"target"`
	Fingerprint string              `json:"fingerprint"`
	Plugins     []pluginApplyResult `json:"plugins"`
}

type pluginApplyResult struct {
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Status       string   `json:"status"`
	Error        string   `json:"error,omitempty"`
	Command      []string `json:"command,omitempty"`
	Instructions []string `json:"instructions,omitempty"`
}

type pluginNameList []string

func (names *pluginNameList) String() string {
	return strings.Join(*names, ",")
}

func (names *pluginNameList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("plugin name cannot be empty")
	}
	*names = append(*names, value)
	return nil
}

const pluginApplyUsage = "Usage: ai-sync plugins apply <project-dir> --target codex|kiro --fingerprint <fingerprint> (--all | --plugin <name>)\n\n" +
	"Applies the actions of `ai-sync plugins preview` for one target and reports one outcome per plugin.\n" +
	"The project is inspected again and the apply refuses a fingerprint that is no longer current, so the\n" +
	"reviewed effects are the ones installed. A blocked plugin stops the whole apply until its conflict is\n" +
	"resolved. A Kiro package runs `kiro-cli powers install`; Codex reports the marketplace entry and the\n" +
	"`codex plugin add` step because registration happens in the agent. Plugins whose package still needs\n" +
	"preparation or that could not be inspected are reported and left pending.\n"

func applyPlugins(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return pluginApplyUsage, nil
	}
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return "", errors.New("usage: ai-sync plugins apply <project-dir> --target codex|kiro --fingerprint <fingerprint> (--all | --plugin <name>)")
	}
	project := args[0]

	flags := flag.NewFlagSet("ai-sync plugins apply", flag.ContinueOnError)
	var flagOutput bytes.Buffer
	flags.SetOutput(&flagOutput)
	var target string
	var fingerprint string
	var all bool
	var names pluginNameList
	flags.StringVar(&target, "target", "", "target to apply: codex or kiro")
	flags.StringVar(&fingerprint, "fingerprint", "", "fingerprint reported by ai-sync plugins preview for this target")
	flags.BoolVar(&all, "all", false, "apply every enabled plugin declared for the target")
	flags.Var(&names, "plugin", "logical plugin name to apply; repeat to select more than one")
	if err := flags.Parse(args[1:]); err != nil {
		output := flagOutput.String()
		if errors.Is(err, flag.ErrHelp) {
			return output, nil
		}
		return strings.TrimSpace(output), err
	}
	if flags.NArg() > 0 {
		return "", fmt.Errorf("unknown apply argument %q", flags.Arg(0))
	}
	if target != "codex" && target != "kiro" {
		return "", errors.New("usage: ai-sync plugins apply <project-dir> --target codex|kiro --fingerprint <fingerprint> (--all | --plugin <name>)")
	}
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("--fingerprint is required: pass the SHA-256 fingerprint of the preview this apply was reviewed against")
	}
	if all == (len(names) > 0) {
		return "", errors.New("select the plugins to apply with --all or with one or more --plugin <name>")
	}
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err = filepath.Abs(project)
	if err != nil {
		return "", err
	}

	raw, err := previewPlugins(workdir, []string{project, "--target", target})
	if err != nil {
		return "", err
	}
	var preview pluginPreview
	if err := json.Unmarshal([]byte(raw), &preview); err != nil {
		return "", fmt.Errorf("decode preview: %w", err)
	}
	if preview.Fingerprint != fingerprint {
		return "", fmt.Errorf("stale preview: target %s changed since fingerprint %s; run ai-sync plugins preview again and review the new effects", target, fingerprint)
	}

	selected, err := selectedPluginNames(preview, target, all, names)
	if err != nil {
		return "", err
	}
	var blocked []string
	for _, plugin := range preview.Plugins {
		if !selected[plugin.Name] {
			continue
		}
		if len(plugin.Conflicts) > 0 || plugin.Action.Kind == "blocked" {
			blocked = append(blocked, plugin.Name+" ("+strings.Join(plugin.Conflicts, "; ")+")")
		}
	}
	if len(blocked) > 0 {
		return "", fmt.Errorf("cannot apply: resolve the conflict for %s with a distinct native identity or package path, then preview again", strings.Join(blocked, ", "))
	}

	doc := pluginApplyDoc{Target: target, Fingerprint: preview.Fingerprint, Plugins: []pluginApplyResult{}}
	var failed []string
	for _, plugin := range preview.Plugins {
		if !selected[plugin.Name] {
			continue
		}
		result := pluginApplyResult{Name: plugin.Name, State: plugin.State, Instructions: plugin.Action.Instructions}
		switch {
		case !plugin.Enabled:
			result.Status = "disabled"
		case plugin.Action.Kind == "command":
			result.Command = plugin.Action.Command
			if err := runPluginInstall(plugin.Action.Command); err != nil {
				result.Status = "failed"
				result.Error = err.Error()
				failed = append(failed, plugin.Name)
			} else {
				result.Status = "installed"
			}
		case plugin.Action.Kind == "manual":
			result.Status = "manual"
		case plugin.Action.Kind == "pending":
			result.Status = "pending"
			result.Error = "a target-specific package with the omitted components removed must be prepared and validated before installing"
		default:
			result.Status = plugin.State
			result.Error = pluginUninstallableReason(plugin)
		}
		doc.Plugins = append(doc.Plugins, result)
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if len(failed) > 0 {
		return string(encoded) + "\n", fmt.Errorf("plugin installation failed for %s", strings.Join(failed, ", "))
	}
	return string(encoded) + "\n", nil
}

func selectedPluginNames(preview pluginPreview, target string, all bool, names pluginNameList) (map[string]bool, error) {
	selected := map[string]bool{}
	if all {
		for _, plugin := range preview.Plugins {
			if plugin.Enabled {
				selected[plugin.Name] = true
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("no enabled plugin is declared for target %s", target)
		}
		return selected, nil
	}
	declared := map[string]bool{}
	for _, plugin := range preview.Plugins {
		declared[plugin.Name] = true
	}
	for _, name := range names {
		if !declared[name] {
			return nil, fmt.Errorf("plugin %q has no variant for target %s", name, target)
		}
		selected[name] = true
	}
	return selected, nil
}

func pluginUninstallableReason(plugin previewPlugin) string {
	if len(plugin.Omitted) > 0 {
		return strings.Join(plugin.Omitted, "; ")
	}
	if len(plugin.Missing) > 0 {
		return strings.Join(plugin.Missing, "; ")
	}
	return plugin.Action.Kind
}

func runPluginInstall(command []string) error {
	if len(command) == 0 {
		return errors.New("empty install command")
	}
	binary, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("%s is not available in PATH", command[0])
	}
	install := exec.Command(binary, command[1:]...)
	var stderr bytes.Buffer
	install.Stderr = &stderr
	if err := install.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return fmt.Errorf("%v: %s", err, message)
		}
		return err
	}
	return nil
}
