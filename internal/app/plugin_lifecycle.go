package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type installedClaudePlugin struct {
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
	InstallPath string `json:"installPath"`
}

type pluginLifecycleItem struct {
	Name             string                   `json:"name"`
	Enabled          bool                     `json:"enabled"`
	DeclaredTargets  []string                 `json:"declared_targets"`
	ClaudeIdentity   string                   `json:"claude_identity,omitempty"`
	ClaudeInstalled  bool                     `json:"claude_installed"`
	InstalledVersion string                   `json:"installed_version,omitempty"`
	AvailableVersion string                   `json:"available_version,omitempty"`
	UpdateState      string                   `json:"update_state,omitempty"`
	Variants         []inventoryPluginVariant `json:"variants"`
}

type pluginLifecycleDocument struct {
	Project string                `json:"project"`
	Plugins []pluginLifecycleItem `json:"plugins"`
}

func managePlugins(workdir string, args []string) (string, error) {
	if len(args) < 2 || args[1] == "--help" || args[1] == "-h" {
		return "Usage: ai-sync plugins status <project-dir>\n       ai-sync plugins updates <project-dir> [--refresh]\n       ai-sync plugins add|update|remove <project-dir> <plugin@marketplace> --target claude [--repo owner/repo|--url URL|--path PATH]\n", nil
	}
	project := args[1]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	var err error
	project, err = filepath.Abs(project)
	if err != nil {
		return "", err
	}
	switch args[0] {
	case "status":
		if len(args) != 2 {
			return "", errors.New("plugins status takes exactly one project directory")
		}
		return pluginLifecycleStatus(project, false)
	case "updates":
		if len(args) == 3 && args[2] == "--refresh" {
			if err := refreshClaudeMarketplaces(project); err != nil {
				return "", err
			}
		} else if len(args) != 2 {
			return "", errors.New("usage: ai-sync plugins updates <project-dir> [--refresh]")
		}
		return pluginLifecycleStatus(project, true)
	case "add", "update", "remove":
		return mutateClaudePlugin(project, args[0], args[2:])
	default:
		return "", fmt.Errorf("unknown plugin operation %q", args[0])
	}
}

func refreshClaudeMarketplaces(project string) error {
	config, err := loadPlugins(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil {
		return err
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("claude not found in PATH")
	}
	marketplaces := map[string]bool{}
	for identity := range config.Plugins {
		_, name, ok := strings.Cut(identity, "@")
		if ok && config.Marketplaces[name].Path == "" {
			marketplaces[name] = true
		}
	}
	for _, name := range sortedStringMapKeys(marketplaces) {
		cmd := exec.Command(path, "plugin", "marketplace", "update", name)
		cmd.Dir = project
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("refresh Claude marketplace %s: %w: %s", name, err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func pluginLifecycleStatus(project string, checkUpdates bool) (string, error) {
	raw, err := inventoryProject(project, []string{project})
	if err != nil {
		return raw, err
	}
	var inventory inventoryResult
	if err := json.Unmarshal([]byte(raw), &inventory); err != nil {
		return "", err
	}
	if inventory.Status != "ok" {
		return "", fmt.Errorf("project inventory is %s: %v", inventory.Status, inventory.Diagnostics)
	}
	installed, err := readInstalledClaudePlugins()
	if err != nil {
		return "", err
	}
	doc := pluginLifecycleDocument{Project: project, Plugins: []pluginLifecycleItem{}}
	for _, resource := range inventory.Resources {
		if resource.Type != "plugin" {
			continue
		}
		item := pluginLifecycleItem{Name: resource.Name, Enabled: resource.Enabled != nil && *resource.Enabled, Variants: resource.Variants, DeclaredTargets: []string{}}
		if item.Variants == nil {
			item.Variants = []inventoryPluginVariant{}
		}
		if len(resource.Variants) == 0 {
			item.DeclaredTargets = append(item.DeclaredTargets, "claude")
			item.ClaudeIdentity = resource.Name
		}
		for _, variant := range resource.Variants {
			item.DeclaredTargets = append(item.DeclaredTargets, variant.Target)
			if variant.Target == "claude" {
				item.ClaudeIdentity = variant.Source.Install
			}
		}
		if item.ClaudeIdentity != "" {
			for _, entry := range installed[item.ClaudeIdentity] {
				if entry.Scope == "project" && sameProjectPath(entry.ProjectPath, project) {
					item.ClaudeInstalled = true
					item.InstalledVersion = entry.Version
					break
				}
			}
			if checkUpdates {
				item.AvailableVersion = availableClaudePluginVersion(item.ClaudeIdentity)
				item.UpdateState = comparePluginVersions(item.InstalledVersion, item.AvailableVersion)
			}
		}
		doc.Plugins = append(doc.Plugins, item)
	}
	sort.Slice(doc.Plugins, func(i, j int) bool { return doc.Plugins[i].Name < doc.Plugins[j].Name })
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

func sameProjectPath(a, b string) bool {
	if a == "" {
		return false
	}
	a, errA := filepath.EvalSymlinks(a)
	b, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return a == b
}

func readInstalledClaudePlugins() (map[string][]installedClaudePlugin, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]installedClaudePlugin{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Claude plugin installations: %w", err)
	}
	var registry struct {
		Plugins map[string][]installedClaudePlugin `json:"plugins"`
	}
	if err := json.Unmarshal(contents, &registry); err != nil {
		return nil, fmt.Errorf("parse Claude plugin installations: %w", err)
	}
	return registry.Plugins, nil
}

func availableClaudePluginVersion(identity string) string {
	plugin, marketplace, ok := strings.Cut(identity, "@")
	if !ok || plugin == "" || marketplace == "" || strings.ContainsAny(marketplace, `/\\`) {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "plugins", "marketplaces", marketplace)
	contents, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Plugins []struct {
			Name    string          `json:"name"`
			Version string          `json:"version"`
			Source  json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if json.Unmarshal(contents, &manifest) != nil {
		return ""
	}
	for _, entry := range manifest.Plugins {
		if entry.Name != plugin {
			continue
		}
		if entry.Version != "" {
			return entry.Version
		}
		var source string
		if json.Unmarshal(entry.Source, &source) != nil || source == "" || filepath.IsAbs(source) {
			return ""
		}
		clean := filepath.Clean(source)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return ""
		}
		pluginRoot := filepath.Join(root, clean)
		for _, name := range []string{filepath.Join(".claude-plugin", "plugin.json"), "plugin.json"} {
			data, err := os.ReadFile(filepath.Join(pluginRoot, name))
			if err != nil {
				continue
			}
			var pluginManifest struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &pluginManifest) == nil && pluginManifest.Version != "" {
				return pluginManifest.Version
			}
		}
	}
	return ""
}

func comparePluginVersions(installed, available string) string {
	if installed == "" {
		return "not_installed"
	}
	if available == "" {
		return "unknown"
	}
	if installed == available {
		return "current"
	}
	installedParts, installedOK := numericVersion(installed)
	availableParts, availableOK := numericVersion(available)
	if installedOK && availableOK {
		for i := 0; i < len(installedParts) || i < len(availableParts); i++ {
			var old, next int
			if i < len(installedParts) {
				old = installedParts[i]
			}
			if i < len(availableParts) {
				next = availableParts[i]
			}
			if next > old {
				return "update_available"
			}
			if next < old {
				return "local_newer"
			}
		}
		return "current"
	}
	return "different"
}

func numericVersion(version string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) == 0 {
		return nil, false
	}
	values := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}

func mutateClaudePlugin(project, operation string, args []string) (string, error) {
	if len(args) < 3 || args[1] != "--target" || args[2] != "claude" {
		return "", errors.New("usage: ai-sync plugins add|update|remove <project-dir> <plugin@marketplace> --target claude")
	}
	identity := args[0]
	if err := validatePluginKey(identity); err != nil {
		return "", err
	}
	if operation != "add" && len(args) != 3 {
		return "", fmt.Errorf("plugins %s accepts no source flags", operation)
	}
	configPath := filepath.Join(project, ".ai", "plugins.yaml")
	config, err := loadPlugins(configPath)
	if err != nil {
		return "", err
	}
	_, declared := config.Plugins[identity]
	if operation == "add" && declared {
		return "", fmt.Errorf("plugin %q is already declared", identity)
	}
	if operation != "add" && !declared {
		return "", fmt.Errorf("plugin %q is not declared for Claude", identity)
	}
	if operation == "update" {
		if !isPluginInstalled(identity, project) {
			return "", fmt.Errorf("plugin %q is not installed in this project; run ai-sync or plugins add first", identity)
		}
		return runClaudePluginCommand(project, "update", identity)
	}
	if operation == "remove" {
		for logical, entry := range config.Portable {
			if entry.Targets["claude"].Install == identity {
				return "", fmt.Errorf("plugin %q belongs to structured declaration %q; edit its targets before removal", identity, logical)
			}
		}
		if isPluginInstalled(identity, project) {
			if _, err := runClaudePluginCommand(project, "uninstall", identity); err != nil {
				return "", err
			}
		}
	}
	root, contents, err := readPluginYAML(configPath)
	if err != nil {
		return "", err
	}
	plugins := yamlMapping(root, "plugins", true)
	if operation == "add" {
		_, marketplace, _ := strings.Cut(identity, "@")
		if err := addPluginDeclaration(root, plugins, marketplace, identity, args[3:]); err != nil {
			return "", err
		}
	} else {
		deleteYAMLKey(plugins, identity)
	}
	if err := writePluginYAML(configPath, root); err != nil {
		return "", err
	}
	outputs, err := renderOutputs(project, "claude")
	if err == nil {
		_, err = applyOutputs(project, outputs, false)
	}
	if err != nil {
		os.WriteFile(configPath, contents, 0644)
		return "", fmt.Errorf("sync Claude configuration: %w", err)
	}
	if operation == "add" {
		return runClaudePluginCommand(project, "install", identity)
	}
	return fmt.Sprintf("removed %s from .ai/plugins.yaml and Claude project installation\n", identity), nil
}

func runClaudePluginCommand(project, operation, identity string) (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude not found in PATH")
	}
	args := []string{"plugin", operation, identity, "--scope", "project", "--json"}
	if operation == "install" {
		args = append(args, "--yes")
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = project
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("claude plugin %s %s: %w", operation, identity, err)
	}
	return string(output), nil
}

func readPluginYAML(path string) (*yaml.Node, []byte, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		contents = []byte("{}\n")
	} else if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(contents, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return doc.Content[0], contents, nil
}

func yamlMapping(parent *yaml.Node, key string, create bool) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	if !create {
		return nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child
}

func deleteYAMLKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func addPluginDeclaration(root, plugins *yaml.Node, marketplace, identity string, flags []string) error {
	marketplaces := yamlMapping(root, "marketplaces", true)
	if yamlMapping(marketplaces, marketplace, false) == nil {
		if len(flags) != 2 {
			return fmt.Errorf("new marketplace %q needs --repo, --url, or --path", marketplace)
		}
		field := strings.TrimPrefix(flags[0], "--")
		if field != "repo" && field != "url" && field != "path" || flags[1] == "" {
			return errors.New("expected --repo owner/repo, --url URL, or --path PATH")
		}
		entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		entry.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: field}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: flags[1]}}
		marketplaces.Content = append(marketplaces.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: marketplace}, entry)
	} else if len(flags) != 0 {
		return fmt.Errorf("marketplace %q is already declared; omit its source flags", marketplace)
	}
	plugins.Content = append(plugins.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: identity}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	return nil
}

func writePluginYAML(path string, root *yaml.Node) error {
	if root.Kind != yaml.MappingNode {
		return errors.New(".ai/plugins.yaml root must be a mapping")
	}
	contents, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0644)
}
