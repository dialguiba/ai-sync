package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func pluginTUICommand(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync plugins tui <project-dir>\n", nil
	}
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return "", errors.New("usage: ai-sync plugins tui <project-dir>")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	if !terminalFile(os.Stdin) || !terminalFile(os.Stdout) {
		return "", errors.New("plugins tui needs an interactive terminal; use plugins status for JSON output")
	}
	return "", runPluginTUI(project, os.Stdin, os.Stdout, true)
}

func terminalFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runPluginTUI(project string, input io.Reader, output io.Writer, clear bool) error {
	reader := bufio.NewReader(input)
	message := ""
	for {
		raw, err := pluginLifecycleStatus(project, true)
		if err != nil {
			return err
		}
		var status pluginLifecycleDocument
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			return err
		}
		if clear {
			fmt.Fprint(output, "\x1b[H\x1b[2J")
		}
		fmt.Fprintf(output, "ai-sync plugins  %s\n\n", project)
		if message != "" {
			fmt.Fprintln(output, message+"\n")
			message = ""
		}
		if len(status.Plugins) == 0 {
			fmt.Fprintln(output, "No plugins declared.")
		}
		for i, plugin := range status.Plugins {
			state := "disabled"
			if plugin.Enabled {
				state = "declared"
			}
			if plugin.ClaudeIdentity != "" {
				if plugin.ClaudeInstalled {
					state = "Claude installed " + plugin.InstalledVersion
					if plugin.UpdateState == "update_available" {
						state += " → " + plugin.AvailableVersion
					}
				} else {
					state = "Claude not installed"
				}
			}
			fmt.Fprintf(output, "%d. %s  [%s]  %s\n", i+1, plugin.Name, strings.Join(plugin.DeclaredTargets, ", "), state)
		}
		fmt.Fprintln(output, "\na. Add Claude plugin    r. Refresh marketplaces    q. Quit")
		choice, err := tuiRead(reader, output, "> ")
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "q":
			return nil
		case "a":
			message, err = tuiAddClaude(project, reader, output)
		case "r":
			err = refreshClaudeMarketplaces(project)
			if err == nil {
				message = "Marketplaces refreshed."
			}
		default:
			index, parseErr := strconv.Atoi(choice)
			if parseErr != nil || index < 1 || index > len(status.Plugins) {
				message = "Choose a listed number or action."
				continue
			}
			message, err = tuiPlugin(project, status.Plugins[index-1], reader, output)
		}
		if err != nil {
			message = "Error: " + err.Error()
		}
	}
}

func tuiRead(reader *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := fmt.Fprint(output, prompt); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) && line != "" {
		return strings.TrimSpace(line), nil
	}
	return strings.TrimSpace(line), err
}

func tuiAddClaude(project string, reader *bufio.Reader, output io.Writer) (string, error) {
	identity, err := tuiRead(reader, output, "Plugin (name@marketplace, blank to cancel): ")
	if err != nil || identity == "" {
		return "", err
	}
	if err := validatePluginKey(identity); err != nil {
		return "", err
	}
	config, err := loadPlugins(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil {
		return "", err
	}
	args := []string{identity, "--target", "claude"}
	_, marketplace, _ := strings.Cut(identity, "@")
	if _, exists := config.Marketplaces[marketplace]; !exists {
		kind, err := tuiRead(reader, output, "New marketplace source (repo/url/path, blank to cancel): ")
		if err != nil || kind == "" {
			return "", err
		}
		if kind != "repo" && kind != "url" && kind != "path" {
			return "", errors.New("source must be repo, url, or path")
		}
		value, err := tuiRead(reader, output, "Source value: ")
		if err != nil || value == "" {
			return "", err
		}
		args = append(args, "--"+kind, value)
	}
	confirm, err := tuiRead(reader, output, "Declare and install for this project? [y/N] ")
	if err != nil || !strings.EqualFold(confirm, "y") {
		return "", err
	}
	return mutateClaudePlugin(project, "add", args)
}

func tuiPlugin(project string, plugin pluginLifecycleItem, reader *bufio.Reader, output io.Writer) (string, error) {
	fmt.Fprintf(output, "\n%s\n", plugin.Name)
	for _, variant := range plugin.Variants {
		fmt.Fprintf(output, "  %s: %s", variant.Target, variant.Status)
		if variant.Version != "" {
			fmt.Fprint(output, " v"+variant.Version)
		}
		fmt.Fprintln(output)
	}
	config, err := loadPlugins(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil {
		return "", err
	}
	_, simpleClaude := config.Plugins[plugin.Name]
	var portableTargets []string
	for _, target := range plugin.DeclaredTargets {
		if target == "codex" || target == "kiro" {
			portableTargets = append(portableTargets, target)
		}
	}
	if simpleClaude {
		fmt.Fprintln(output, "u. Update Claude plugin    d. Remove Claude plugin")
	}
	if len(portableTargets) > 0 {
		fmt.Fprintln(output, "p. Preview/apply Codex or Kiro variant")
	}
	choice, err := tuiRead(reader, output, "b. Back  > ")
	if err != nil {
		return "", err
	}
	switch strings.ToLower(choice) {
	case "u":
		if simpleClaude {
			return mutateClaudePlugin(project, "update", []string{plugin.Name, "--target", "claude"})
		}
	case "d":
		if simpleClaude {
			confirm, err := tuiRead(reader, output, "Remove declaration and project installation? Type remove: ")
			if err != nil || confirm != "remove" {
				return "", err
			}
			return mutateClaudePlugin(project, "remove", []string{plugin.Name, "--target", "claude"})
		}
	case "p":
		if len(portableTargets) > 0 {
			return tuiPreviewApply(project, plugin.Name, portableTargets, reader, output)
		}
	}
	return "", nil
}

func tuiPreviewApply(project, name string, targets []string, reader *bufio.Reader, output io.Writer) (string, error) {
	target := targets[0]
	if len(targets) > 1 {
		choice, err := tuiRead(reader, output, "Target ("+strings.Join(targets, "/")+"): ")
		if err != nil {
			return "", err
		}
		if choice != targets[0] && choice != targets[1] {
			return "", nil
		}
		target = choice
	}
	raw, err := previewPlugins(project, []string{project, "--target", target})
	if err != nil {
		return "", err
	}
	var preview pluginPreview
	if err := json.Unmarshal([]byte(raw), &preview); err != nil {
		return "", err
	}
	for _, plugin := range preview.Plugins {
		if plugin.Name != name {
			continue
		}
		fmt.Fprintf(output, "\n%s: %s\nAction: %s\n", target, plugin.State, plugin.Action.Kind)
		for _, detail := range append(append(append([]string{}, plugin.Missing...), plugin.Omitted...), plugin.Conflicts...) {
			fmt.Fprintln(output, "  "+detail)
		}
		for _, instruction := range plugin.Action.Instructions {
			fmt.Fprintln(output, "  "+instruction)
		}
		if plugin.Action.Kind == "command" {
			fmt.Fprintln(output, "  "+strings.Join(plugin.Action.Command, " "))
		}
		if !plugin.Enabled || plugin.Action.Kind == "none" || plugin.Action.Kind == "blocked" || plugin.Action.Kind == "pending" {
			return "No applicable action for this variant.", nil
		}
		confirm, err := tuiRead(reader, output, "Apply this reviewed action? [y/N] ")
		if err != nil || !strings.EqualFold(confirm, "y") {
			return "", err
		}
		return applyPlugins(project, []string{project, "--target", target, "--fingerprint", preview.Fingerprint, "--plugin", name})
	}
	return "", fmt.Errorf("plugin %q has no %s variant", name, target)
}
