package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type inventoryResource struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Status       string   `json:"status,omitempty"`
	Sources      []string `json:"sources"`
	Destinations []string `json:"destinations"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

type inventoryDiagnostic struct {
	Path    string `json:"path"`
	Unit    string `json:"unit,omitempty"`
	Message string `json:"message"`
}

type inventoryResult struct {
	Status              string                       `json:"status"`
	Resources           []inventoryResource          `json:"resources"`
	PluginConfiguration inventoryPluginConfiguration `json:"plugin_configuration"`
	AdditionalFiles     []string                     `json:"additional_files"`
	Diagnostics         []inventoryDiagnostic        `json:"diagnostics"`
}

type inventoryPluginConfiguration struct {
	Marketplaces   map[string]map[string]any `json:"marketplaces"`
	SharedEnvNames []string                  `json:"shared_env_names"`
}

func inventoryProject(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync inventory <project-dir>\nPrint a read-only JSON inventory of the project's .ai/ directory.\n", nil
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("inventory requires exactly one explicit project directory: ai-sync inventory <project-dir>")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	result := inventoryResult{
		Status: "ok", Resources: []inventoryResource{},
		PluginConfiguration: inventoryPluginConfiguration{Marketplaces: map[string]map[string]any{}, SharedEnvNames: []string{}},
		AdditionalFiles:     []string{}, Diagnostics: []inventoryDiagnostic{},
	}
	root := filepath.Join(project, ".ai")
	info, err := os.Stat(root)
	if err != nil {
		result.Status = "invalid"
		if os.IsNotExist(err) {
			result.Status = "missing"
		}
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: inventoryCause(err)})
		return encodeInventory(result)
	}
	if !info.IsDir() {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: "expected a directory"})
		result.Status = "invalid"
		return encodeInventory(result)
	}

	known := map[string]bool{}
	addInstruction := func(kind, name, source, destination string) {
		path := filepath.Join(project, filepath.FromSlash(source))
		info, err := os.Stat(path)
		if err != nil {
			if kind == "shared_instruction" || !os.IsNotExist(err) {
				result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			}
			return
		}
		if !info.Mode().IsRegular() {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: "expected a regular file"})
			return
		}
		if _, err := os.ReadFile(path); err != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			return
		}
		known[source] = true
		result.Resources = append(result.Resources, inventoryResource{
			Type: kind, Name: name, Sources: []string{source}, Destinations: []string{destination},
		})
	}
	addInstruction("shared_instruction", "project", ".ai/project.md", "CLAUDE.md")
	if len(result.Resources) > 0 {
		result.Resources[0].Destinations = []string{".kiro/steering/project-conventions.md", "AGENTS.md", "CLAUDE.md"}
	}
	for _, target := range []struct{ name, destination string }{
		{"claude", "CLAUDE.md"}, {"codex", "AGENTS.md"}, {"kiro", ".kiro/steering/project-conventions.md"},
	} {
		addInstruction("agent_instruction", target.name, ".ai/targets/"+target.name+".md", target.destination)
	}
	result.Resources = append(result.Resources, inventoryPathRules(root, known, &result.Diagnostics)...)
	result.Resources = append(result.Resources, inventoryAgents(root, known, &result.Diagnostics)...)
	inventorySkills(project, &result, known)
	mcpPath := ".ai/mcp.yaml"
	if contents, err := os.ReadFile(filepath.Join(root, "mcp.yaml")); err != nil {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: mcpPath, Message: inventoryCause(err)})
	} else {
		var config mcpConfig
		if err := yaml.Unmarshal(contents, &config); err != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: mcpPath, Message: err.Error()})
		} else {
			known[mcpPath] = true
			for _, name := range sortedServerNames(config.Servers) {
				if strings.TrimSpace(name) == "" {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: mcpPath, Unit: name, Message: "server name must not be empty"})
					continue
				}
				if strings.TrimSpace(config.Servers[name].Command) == "" {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: mcpPath, Unit: name, Message: "server command must not be empty"})
					continue
				}
				result.Resources = append(result.Resources, inventoryResource{
					Type: "mcp_server", Name: name, Sources: []string{mcpPath},
					Destinations: []string{".codex/config.toml", ".kiro/settings/mcp.json", ".mcp.json"},
				})
			}
		}
	}
	pluginsPath := ".ai/plugins.yaml"
	if contents, err := os.ReadFile(filepath.Join(root, "plugins.yaml")); err == nil {
		var config pluginConfig
		if err := yaml.Unmarshal(contents, &config); err != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: pluginsPath, Message: err.Error()})
		} else {
			known[pluginsPath] = true
			for _, name := range sortedStringMapKeys(config.Marketplaces) {
				result.PluginConfiguration.Marketplaces[name] = marketplaceSourceJSON(config.Marketplaces[name])
			}
			result.PluginConfiguration.SharedEnvNames = sortedStringMapKeys(config.Env)
			for _, name := range sortedStringMapKeys(config.Marketplaces) {
				if err := validateMarketplace(name, config.Marketplaces[name]); err != nil {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: pluginsPath, Unit: name, Message: err.Error()})
				}
			}
			for _, name := range sortedStringMapKeys(config.Plugins) {
				if err := validatePluginKey(name); err != nil {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: pluginsPath, Unit: name, Message: err.Error()})
					continue
				}
				enabled := config.Plugins[name]
				result.Resources = append(result.Resources, inventoryResource{
					Type: "plugin", Name: name, Sources: []string{pluginsPath},
					Destinations: []string{".claude/settings.json"}, Enabled: &enabled,
				})
			}
		}
	} else if !os.IsNotExist(err) {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: pluginsPath, Message: inventoryCause(err)})
	}
	for _, declaration := range []struct {
		path, kind   string
		destinations []string
	}{
		{".ai/hooks.yaml", "hook_event", []string{".claude/settings.json"}},
		{".ai/setup.yaml", "setup_entry", []string{}},
	} {
		resources, diagnostics := inventoryNamedYAML(project, declaration.path, declaration.kind, declaration.destinations)
		result.Resources = append(result.Resources, resources...)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		if len(resources) > 0 {
			known[declaration.path] = true
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		rel, relErr := filepath.Rel(project, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if walkErr != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: inventoryCause(walkErr)})
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !known[rel] {
			result.AdditionalFiles = append(result.AdditionalFiles, rel)
		}

		return nil
	}); err != nil {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: inventoryCause(err)})
	}
	sort.Strings(result.AdditionalFiles)
	resourceOrder := map[string]int{
		"shared_instruction": 0, "agent_instruction": 1, "path_rule": 2, "agent": 3,
		"skill": 4, "mcp_server": 5, "plugin": 6, "hook_event": 7, "setup_entry": 8,
	}
	sort.Slice(result.Resources, func(i, j int) bool {
		left, right := result.Resources[i], result.Resources[j]
		if resourceOrder[left.Type] != resourceOrder[right.Type] {
			return resourceOrder[left.Type] < resourceOrder[right.Type]
		}
		return left.Name < right.Name
	})
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		left, right := result.Diagnostics[i], result.Diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Unit != right.Unit {
			return left.Unit < right.Unit
		}
		return left.Message < right.Message
	})
	if len(result.Diagnostics) > 0 {
		result.Status = "invalid"
	}
	return encodeInventory(result)
}

func inventoryPathRules(root string, known map[string]bool, diagnostics *[]inventoryDiagnostic) []inventoryResource {
	entries, err := os.ReadDir(filepath.Join(root, "rules"))
	if err != nil {
		if !os.IsNotExist(err) {
			*diagnostics = append(*diagnostics, inventoryDiagnostic{Path: ".ai/rules", Message: inventoryCause(err)})
		}
		return nil
	}
	var resources []inventoryResource
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		source := ".ai/rules/" + entry.Name()
		contents, err := os.ReadFile(filepath.Join(root, "rules", entry.Name()))
		if err != nil {
			*diagnostics = append(*diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		rule, err := parsePathRule(name, contents)
		if err != nil {
			*diagnostics = append(*diagnostics, inventoryDiagnostic{Path: source, Message: err.Error()})
			continue
		}
		known[source] = true
		destinations := []string{".claude/rules/" + entry.Name(), ".kiro/steering/" + entry.Name()}
		scopes := map[string]bool{}
		for _, pattern := range rule.Paths {
			dir := codexScopeDir(pattern)
			if dir == "" {
				scopes["AGENTS.md"] = true
			} else {
				scopes[dir+"/AGENTS.md"] = true
			}
		}
		for _, destination := range sortedStringMapKeys(scopes) {
			destinations = append(destinations, destination)
		}
		resources = append(resources, inventoryResource{Type: "path_rule", Name: name, Sources: []string{source}, Destinations: destinations})
	}
	return resources
}

func inventoryAgents(root string, known map[string]bool, diagnostics *[]inventoryDiagnostic) []inventoryResource {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		if !os.IsNotExist(err) {
			*diagnostics = append(*diagnostics, inventoryDiagnostic{Path: ".ai/agents", Message: inventoryCause(err)})
		}
		return nil
	}
	var resources []inventoryResource
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		source := ".ai/agents/" + entry.Name()
		if _, err := os.ReadFile(filepath.Join(root, "agents", entry.Name())); err != nil {
			*diagnostics = append(*diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			continue
		}
		known[source] = true
		resources = append(resources, inventoryResource{
			Type: "agent", Name: strings.TrimSuffix(entry.Name(), ".md"),
			Sources: []string{source}, Destinations: []string{".claude/agents/" + entry.Name()},
		})
	}
	return resources
}

func inventorySkills(project string, result *inventoryResult, known map[string]bool) {
	root := filepath.Join(project, ".ai", "skills")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai/skills", Message: inventoryCause(err)})
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		resource := inventoryResource{Type: "skill", Name: name, Status: "ok", Sources: []string{}, Destinations: []string{}}
		skillRoot := filepath.Join(root, name)
		err := filepath.WalkDir(skillRoot, func(path string, file fs.DirEntry, walkErr error) error {
			rel, relErr := filepath.Rel(project, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if walkErr != nil {
				result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: inventoryCause(walkErr)})
				resource.Status = "invalid"
				return nil
			}
			if file.IsDir() {
				return nil
			}
			known[rel] = true
			resource.Sources = append(resource.Sources, rel)
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: inventoryCause(readErr)})
				resource.Status = "invalid"
			} else if file.Name() == "SKILL.md" && path == filepath.Join(skillRoot, "SKILL.md") {
				if err := validateInventorySkill(contents); err != nil {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: err.Error()})
					resource.Status = "invalid"
				}
			}
			withinSkill, err := filepath.Rel(skillRoot, path)
			if err != nil {
				return err
			}
			withinSkill = filepath.ToSlash(withinSkill)
			for _, base := range []string{".agents/skills", ".claude/skills", ".kiro/skills"} {
				resource.Destinations = append(resource.Destinations, filepath.ToSlash(filepath.Join(base, name, withinSkill)))
			}
			powerFile := withinSkill
			if withinSkill == "SKILL.md" {
				powerFile = "POWER.md"
			}
			resource.Destinations = append(resource.Destinations, filepath.ToSlash(filepath.Join(".kiro/powers", name, powerFile)))
			return nil
		})
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai/skills/" + name, Message: inventoryCause(err)})
			resource.Status = "invalid"
		}
		if !known[".ai/skills/"+name+"/SKILL.md"] {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai/skills/" + name + "/SKILL.md", Message: "not found"})
			resource.Status = "invalid"
			resource.Destinations = append(resource.Destinations, filepath.ToSlash(filepath.Join(".kiro/powers", name, "POWER.md")))
		}
		sort.Strings(resource.Sources)
		sort.Strings(resource.Destinations)
		result.Resources = append(result.Resources, resource)
	}
}

func validateInventorySkill(contents []byte) error {
	text := strings.ReplaceAll(string(contents), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return errors.New("missing YAML frontmatter")
	}
	frontmatter, _, ok := splitYAMLFrontmatter(text)
	if !ok {
		return errors.New("missing closing YAML frontmatter delimiter")
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Description) == "" {
		return errors.New("YAML frontmatter needs name and description")
	}
	return nil
}

func inventoryNamedYAML(project, source, kind string, destinations []string) ([]inventoryResource, []inventoryDiagnostic) {
	contents, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(source)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []inventoryDiagnostic{{Path: source, Message: inventoryCause(err)}}
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, []inventoryDiagnostic{{Path: source, Message: err.Error()}}
	}
	if len(document.Content) == 0 || document.Content[0].Tag == "!!null" {
		return nil, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, []inventoryDiagnostic{{Path: source, Message: "expected a mapping of named entries"}}
	}
	var resources []inventoryResource
	var diagnostics []inventoryDiagnostic
	seen := map[string]bool{}
	for i := 0; i < len(root.Content); i += 2 {
		nameNode, value := root.Content[i], root.Content[i+1]
		name := nameNode.Value
		if nameNode.Kind != yaml.ScalarNode || nameNode.Tag != "!!str" || strings.TrimSpace(name) == "" {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: source, Unit: name, Message: fmt.Sprintf("%s %q: expected a non-empty name", kind, name)})
			continue
		}
		if seen[name] {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: source, Unit: name, Message: fmt.Sprintf("%s %q: duplicate name", kind, name)})
			continue
		}
		seen[name] = true
		var entryErr error
		switch kind {
		case "hook_event":
			var matchers []hookMatcher
			entryErr = value.Decode(&matchers)
			if entryErr == nil && len(matchers) == 0 {
				entryErr = errors.New("needs at least one matcher with hooks")
			}
			for index, matcher := range matchers {
				if entryErr == nil {
					entryErr = validateHookMatcher(name, index, matcher)
				}
			}
		case "setup_entry":
			var entry setupEntry
			entryErr = value.Decode(&entry)
			if entryErr == nil && (entry.Check == "" || entry.Install == "") {
				entryErr = errors.New("needs a check and an install command")
			}
		default:
			entryErr = fmt.Errorf("unsupported inventory resource type %q", kind)
		}
		if entryErr != nil {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: source, Unit: name, Message: fmt.Sprintf("%s %q: %v", kind, name, entryErr)})
			continue
		}
		resources = append(resources, inventoryResource{Type: kind, Name: name, Sources: []string{source}, Destinations: destinations})
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
	return resources, diagnostics
}

func encodeInventory(result inventoryResult) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	output := string(data) + "\n"
	if result.Status != "ok" {
		return output, fmt.Errorf("inventory source %s", result.Status)
	}
	return output, nil
}

func inventoryCause(err error) string {
	if os.IsNotExist(err) {
		return "not found"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}
