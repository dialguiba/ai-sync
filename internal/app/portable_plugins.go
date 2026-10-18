package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const pluginSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const mcpSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

var envReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var gitCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var pluginName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// A portable declaration has one logical identity and one native package per target.
type portablePlugin struct {
	Enabled bool                    `yaml:"enabled"`
	Targets map[string]pluginTarget `yaml:"targets"`
}

type pluginTarget struct {
	Install string `yaml:"install" json:"install,omitempty"` // Claude marketplace identity.
	Path    string `yaml:"path" json:"path,omitempty"`       // Local package, relative to the project.
	Git     string `yaml:"git" json:"git,omitempty"`
	Ref     string `yaml:"ref" json:"ref,omitempty"`
	Subdir  string `yaml:"subdir" json:"subdir,omitempty"`
}

type inventoryPluginVariant struct {
	Target       string       `json:"target"`
	Status       string       `json:"status"`
	Source       pluginTarget `json:"source"`
	Manifest     string       `json:"manifest,omitempty"`
	PackageName  string       `json:"package_name,omitempty"`
	Version      string       `json:"version,omitempty"`
	Skills       []string     `json:"skills"`
	MCPServers   []string     `json:"mcp_servers"`
	Unsupported  []string     `json:"unsupported"`
	Requirements []string     `json:"requirements"`
}

func decodePluginDeclaration(name string, node *yaml.Node) (portablePlugin, *bool, error) {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	if node.Kind == yaml.ScalarNode {
		if err := validatePluginKey(name); err != nil {
			return portablePlugin{}, nil, err
		}
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return portablePlugin{}, nil, fmt.Errorf("plugin enabled flag must be bool: %w", err)
		}
		return portablePlugin{}, &enabled, nil
	}
	if node.Kind != yaml.MappingNode {
		return portablePlugin{}, nil, fmt.Errorf("plugin declaration must be bool or mapping (YAML kind %d)", node.Kind)
	}
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return portablePlugin{}, nil, fmt.Errorf("invalid logical plugin identity %q", name)
	}
	fields, err := uniqueYAMLFields(node, "plugin")
	if err != nil {
		return portablePlugin{}, nil, err
	}
	for key := range fields {
		if key != "enabled" && key != "targets" {
			return portablePlugin{}, nil, fmt.Errorf("unknown plugin field %q", key)
		}
	}
	if fields["enabled"] == nil || fields["targets"] == nil {
		return portablePlugin{}, nil, errors.New("plugin needs enabled and targets")
	}
	var result portablePlugin
	if err := fields["enabled"].Decode(&result.Enabled); err != nil {
		return portablePlugin{}, nil, fmt.Errorf("enabled must be bool: %w", err)
	}
	targets := fields["targets"]
	if targets.Kind != yaml.MappingNode || len(targets.Content) == 0 {
		return portablePlugin{}, nil, errors.New("targets must be a non-empty mapping")
	}
	result.Targets = map[string]pluginTarget{}
	for i := 0; i < len(targets.Content); i += 2 {
		name, value := targets.Content[i].Value, targets.Content[i+1]
		if name != "claude" && name != "codex" && name != "kiro" {
			return portablePlugin{}, nil, fmt.Errorf("unknown target %q", name)
		}
		if _, exists := result.Targets[name]; exists {
			return portablePlugin{}, nil, fmt.Errorf("duplicate target %q", name)
		}
		if value.Kind != yaml.MappingNode {
			return portablePlugin{}, nil, fmt.Errorf("target %q must be a mapping", name)
		}
		attrs, err := uniqueYAMLFields(value, "target")
		if err != nil {
			return portablePlugin{}, nil, err
		}
		for key := range attrs {
			if key != "install" && key != "path" && key != "git" && key != "ref" && key != "subdir" {
				return portablePlugin{}, nil, fmt.Errorf("unknown target field %q", key)
			}
		}
		var target pluginTarget
		if err := value.Decode(&target); err != nil {
			return portablePlugin{}, nil, err
		}
		if target.Install != "" {
			if name != "claude" {
				return portablePlugin{}, nil, fmt.Errorf("%s: install is Claude-only", name)
			}
			if err := validatePluginKey(target.Install); err != nil {
				return portablePlugin{}, nil, err
			}
		}
		if target.Path != "" && target.Git != "" {
			return portablePlugin{}, nil, fmt.Errorf("%s: choose path or git", name)
		}
		if target.Git != "" && target.Ref == "" {
			return portablePlugin{}, nil, fmt.Errorf("%s: git needs a pinned ref", name)
		}
		if target.Git != "" {
			remote, err := url.Parse(target.Git)
			if err != nil || remote.Scheme != "https" || remote.Host == "" {
				return portablePlugin{}, nil, fmt.Errorf("%s: git must be an HTTPS URL", name)
			}
			if !gitCommit.MatchString(target.Ref) {
				return portablePlugin{}, nil, fmt.Errorf("%s: git ref must be a 40-character commit SHA", name)
			}
		}
		if target.Git == "" && (target.Ref != "" || target.Subdir != "") {
			return portablePlugin{}, nil, fmt.Errorf("%s: ref and subdir require git", name)
		}
		if target.Path == "" && target.Git == "" && target.Install == "" {
			return portablePlugin{}, nil, fmt.Errorf("%s: target needs path, git, or install", name)
		}
		if target.Path != "" && !safePluginPath(target.Path) {
			return portablePlugin{}, nil, fmt.Errorf("%s: path must stay in .ai/", name)
		}
		if target.Subdir != "" && (filepath.IsAbs(target.Subdir) || !safePluginPath(".ai/"+target.Subdir)) {
			return portablePlugin{}, nil, fmt.Errorf("%s: unsafe subdir", name)
		}
		result.Targets[name] = target
	}
	return result, nil, nil
}

func uniqueYAMLFields(node *yaml.Node, context string) (map[string]*yaml.Node, error) {
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate %s field %q", context, key)
		}
		fields[key] = node.Content[i+1]
	}
	return fields, nil
}

func safePluginPath(path string) bool {
	if filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.HasPrefix(clean, ".ai/") && clean != ".ai/.." && !strings.Contains("/"+path+"/", "/../")
}

func inspectPluginTarget(project, name string, targetName string, source pluginTarget, known map[string]bool) (inventoryPluginVariant, []inventoryDiagnostic) {
	v := inventoryPluginVariant{Target: targetName, Source: source, Skills: []string{}, MCPServers: []string{}, Unsupported: []string{}, Requirements: []string{}}
	if source.Path == "" {
		v.Status = "unverified"
		if source.Install != "" {
			v.Requirements = append(v.Requirements, "Claude marketplace package is not available for inspection")
		} else {
			v.Requirements = append(v.Requirements, "Git package must be fetched at pinned ref before installation")
		}
		return v, nil
	}
	root := filepath.Join(project, filepath.FromSlash(source.Path))
	aiRoot, err := filepath.EvalSymlinks(filepath.Join(project, ".ai"))
	if err != nil {
		v.Status = "invalid"
		return v, []inventoryDiagnostic{{Path: source.Path, Unit: name, Message: inventoryCause(err)}}
	}
	projectRoot, err := filepath.EvalSymlinks(project)
	if err != nil || !withinPath(projectRoot, aiRoot) {
		v.Status = "invalid"
		return v, []inventoryDiagnostic{{Path: ".ai", Unit: name, Message: ".ai/ escapes project through symlink"}}
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			v.Status = "unverified"
			v.Requirements = append(v.Requirements, "package path: "+source.Path)
			return v, nil
		}
		v.Status = "invalid"
		return v, []inventoryDiagnostic{{Path: source.Path, Unit: name, Message: inventoryCause(err)}}
	}
	if !withinPath(aiRoot, real) {
		v.Status = "invalid"
		return v, []inventoryDiagnostic{{Path: source.Path, Unit: name, Message: "package path escapes .ai/ through symlink"}}
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		v.Status = "invalid"
		return v, []inventoryDiagnostic{{Path: source.Path, Unit: name, Message: "package path must be a directory"}}
	}
	// Reject all symlinks in an inspectable package. The package can later be copied
	// safely without following links outside the inspected content.
	var diagnostics []inventoryDiagnostic
	filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		rel, _ := filepath.Rel(project, path)
		rel = filepath.ToSlash(rel)
		if walkErr != nil {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: rel, Unit: name, Message: inventoryCause(walkErr)})
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: rel, Unit: name, Message: "symlink in package is unsupported"})
			return nil
		}
		if !entry.IsDir() {
			known[rel] = true
		}
		return nil
	})
	if len(diagnostics) > 0 {
		v.Status = "invalid"
		return v, diagnostics
	}
	manifestPath := filepath.Join(root, "plugin.json")
	claudeOnly := false
	kiroMetadataMissing := false
	claudeManifest := filepath.Join(root, ".claude-plugin", "plugin.json")
	if _, err := os.Stat(claudeManifest); err == nil && targetName == "claude" {
		manifestPath = claudeManifest
		claudeOnly = true
	} else if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		if _, err := os.Stat(claudeManifest); err == nil {
			manifestPath = claudeManifest
			claudeOnly = true
		}
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "plugin.json")), Unit: name, Message: inventoryCause(err)})
	} else {
		var manifest struct {
			Schema      string `json:"$schema"`
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
			Author      struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				URL   string `json:"url"`
			} `json:"author"`
			Homepage   string                                `json:"homepage"`
			Repository string                                `json:"repository"`
			License    string                                `json:"license"`
			Keywords   []string                              `json:"keywords"`
			Extensions map[string]map[string]json.RawMessage `json:"extensions"`
		}
		manifestErr := json.Unmarshal(contents, &manifest)
		if manifestErr == nil && !claudeOnly {
			manifestErr = validateJSONFields(contents, "$schema", "name", "version", "description", "author", "homepage", "repository", "license", "keywords", "extensions")
			if manifestErr == nil {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(contents, &fields)
				if author, exists := fields["author"]; exists {
					manifestErr = validateJSONFields(author, "name", "email", "url")
				}
			}
		}
		if manifestErr != nil || manifest.Name == "" || (!claudeOnly && (manifest.Schema != pluginSchema || len(manifest.Name) > 64 || !pluginName.MatchString(manifest.Name) || strings.Contains(manifest.Name, "--") || strings.Contains(manifest.Name, ".."))) {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "plugin.json")), Unit: name, Message: "invalid Agent Plugins manifest: schema and valid name are required"})
		} else {
			manifestRel, _ := filepath.Rel(project, manifestPath)
			v.Manifest = filepath.ToSlash(manifestRel)
			v.PackageName = manifest.Name
			v.Version = manifest.Version
			if !claudeOnly && targetName == "kiro" {
				if manifest.Version == "" {
					v.Requirements = append(v.Requirements, "Kiro manifest version")
					kiroMetadataMissing = true
				}
				if manifest.Description == "" {
					v.Requirements = append(v.Requirements, "Kiro manifest description")
					kiroMetadataMissing = true
				}
				if manifest.Author.Name == "" {
					v.Requirements = append(v.Requirements, "Kiro manifest author.name")
					kiroMetadataMissing = true
				}
				if len(manifest.Keywords) == 0 {
					v.Requirements = append(v.Requirements, "Kiro manifest keywords")
					kiroMetadataMissing = true
				}
			}
			if claudeOnly && targetName != "claude" {
				v.Unsupported = append(v.Unsupported, "Claude-only manifest")
			}
			for extension := range manifest.Extensions {
				if extension != "com.openai" {
					v.Unsupported = append(v.Unsupported, "extension:"+extension)
				} else if targetName != "codex" {
					v.Unsupported = append(v.Unsupported, "extension:com.openai")
				}
			}
		}
	}
	skillsRoot := filepath.Join(root, "skills")
	if entries, err := os.ReadDir(skillsRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				skillFile := filepath.Join(skillsRoot, entry.Name(), "SKILL.md")
				data, readErr := os.ReadFile(skillFile)
				if readErr != nil || validateInventorySkill(data) != nil {
					diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "skills", entry.Name(), "SKILL.md")), Unit: name, Message: "invalid or missing skill frontmatter"})
				} else {
					v.Skills = append(v.Skills, entry.Name())
				}
			}
		}
	} else if !os.IsNotExist(err) {
		diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "skills")), Unit: name, Message: inventoryCause(err)})
	}
	if data, err := os.ReadFile(filepath.Join(root, "mcp.json")); err == nil {
		var mcp struct {
			Schema  string `json:"$schema"`
			Servers map[string]struct {
				Type    string            `json:"type"`
				URL     string            `json:"url"`
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
				Headers map[string]string `json:"headers"`
				CWD     string            `json:"cwd"`
			} `json:"mcpServers"`
		}
		mcpErr := json.Unmarshal(data, &mcp)
		if mcpErr == nil {
			mcpErr = validateJSONFields(data, "$schema", "mcpServers")
		}
		if mcpErr != nil || mcp.Schema != mcpSchema || mcp.Servers == nil {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "mcp.json")), Unit: name, Message: "invalid Agent Plugins MCP configuration"})
		} else {
			for serverName, server := range mcp.Servers {
				if err := validateMCPServer(data, serverName, server.Type); err != nil {
					diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "mcp.json")), Unit: name, Message: "MCP server " + serverName + ": " + err.Error()})
					continue
				}
				if !(server.Type == "stdio" && server.Command != "" && server.URL == "" || (server.Type == "streamable-http" || server.Type == "sse") && server.URL != "" && server.Command == "") {
					diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "mcp.json")), Unit: name, Message: "MCP server " + serverName + " needs type and url or command"})
					continue
				}
				v.MCPServers = append(v.MCPServers, serverName)
				for _, value := range server.Env {
					missingEnvReferences(value, &v.Requirements)
				}
				for _, value := range server.Headers {
					missingEnvReferences(value, &v.Requirements)
				}
				missingEnvReferences(server.URL, &v.Requirements)
				missingEnvReferences(server.Command, &v.Requirements)
				checkPackageReference(root, server.Command, "MCP command path", &v.Requirements)
				for _, arg := range server.Args {
					missingEnvReferences(arg, &v.Requirements)
					checkPackageReference(root, arg, "MCP argument path", &v.Requirements)
				}
				missingEnvReferences(server.CWD, &v.Requirements)
				checkPackageReference(root, server.CWD, "MCP working directory", &v.Requirements)
			}
		}
	} else if !os.IsNotExist(err) {
		diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, "mcp.json")), Unit: name, Message: inventoryCause(err)})
	}
	if claudeOnly && targetName == "claude" {
		if data, err := os.ReadFile(filepath.Join(root, ".mcp.json")); err == nil {
			var config struct {
				Servers map[string]struct {
					Command string            `json:"command"`
					URL     string            `json:"url"`
					Args    []string          `json:"args"`
					Env     map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if json.Unmarshal(data, &config) != nil || config.Servers == nil {
				diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, ".mcp.json")), Unit: name, Message: "invalid Claude MCP configuration"})
			} else {
				for serverName, server := range config.Servers {
					if server.Command == "" && server.URL == "" {
						diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, ".mcp.json")), Unit: name, Message: "Claude MCP server " + serverName + " needs command or url"})
						continue
					}
					v.MCPServers = append(v.MCPServers, serverName)
					missingEnvReferences(server.Command, &v.Requirements)
					missingEnvReferences(server.URL, &v.Requirements)
					checkPackageReference(root, server.Command, "MCP command path", &v.Requirements)
					for _, arg := range server.Args {
						missingEnvReferences(arg, &v.Requirements)
						checkPackageReference(root, arg, "MCP argument path", &v.Requirements)
					}
					for _, value := range server.Env {
						missingEnvReferences(value, &v.Requirements)
					}
				}
			}
		} else if !os.IsNotExist(err) {
			diagnostics = append(diagnostics, inventoryDiagnostic{Path: filepath.ToSlash(filepath.Join(source.Path, ".mcp.json")), Unit: name, Message: inventoryCause(err)})
		}
	}
	if entries, err := os.ReadDir(root); err == nil {
		knownTopLevel := map[string]bool{"plugin.json": true, "mcp.json": true, "skills": true, "assets": true, ".claude-plugin": true, ".codex-plugin": true, ".mcp.json": true, ".app.json": true, "agents": true, "commands": true, "hooks": true, "scripts": true, "README.md": true, "LICENSE": true, ".git": true}
		for _, entry := range entries {
			if !knownTopLevel[entry.Name()] {
				v.Unsupported = append(v.Unsupported, "unclassified:"+entry.Name())
			}
		}
	}
	for _, component := range []string{".claude-plugin", ".mcp.json", "agents", "commands", ".codex-plugin", "hooks"} {
		if _, err := os.Stat(filepath.Join(root, component)); err == nil {
			if targetName == "kiro" || (targetName == "codex" && component != ".codex-plugin" && component != "hooks") || (targetName == "claude" && component == ".codex-plugin") {
				v.Unsupported = append(v.Unsupported, component)
			} else if targetName == "codex" && component == "hooks" {
				v.Requirements = append(v.Requirements, "Codex desktop manual installation for hooks")
			}
		}
	}
	if targetName == "claude" && source.Install == "" {
		v.Unsupported = append(v.Unsupported, "Claude installation needs marketplace identity")
	}
	if targetName == "claude" && !claudeOnly {
		v.Unsupported = append(v.Unsupported, "Claude-specific manifest unavailable")
	}
	sort.Strings(v.Skills)
	sort.Strings(v.MCPServers)
	sort.Strings(v.Unsupported)
	sort.Strings(v.Requirements)
	v.Requirements = sortedUniqueStrings(v.Requirements)
	switch {
	case len(diagnostics) > 0:
		v.Status = "invalid"
	case claudeOnly && targetName != "claude":
		v.Status = "incompatible"
	case targetName == "claude" && !claudeOnly:
		v.Status = "incompatible"
	case kiroMetadataMissing:
		v.Status = "incompatible"
	case len(v.Requirements) > 0:
		v.Status = "pending"
	case len(v.Unsupported) > 0:
		v.Status = "partial"
	default:
		v.Status = "compatible"
	}
	return v, diagnostics
}

func missingEnvReferences(value string, requirements *[]string) {
	for _, match := range envReference.FindAllStringSubmatch(value, -1) {
		if match[1] != "PLUGIN_ROOT" && match[1] != "PLUGIN_DATA" && os.Getenv(match[1]) == "" {
			*requirements = append(*requirements, "environment variable: "+match[1])
		}
	}
}

func checkPackageReference(root, value, label string, requirements *[]string) {
	if !strings.HasPrefix(value, "./") && !strings.HasPrefix(value, "${PLUGIN_ROOT}/") {
		return
	}
	local := strings.TrimPrefix(strings.TrimPrefix(value, "${PLUGIN_ROOT}/"), "./")
	if !safePackageComponent(local) {
		*requirements = append(*requirements, "unsafe "+label+": "+value)
		return
	}
	if _, err := os.Stat(filepath.Join(root, local)); err != nil {
		*requirements = append(*requirements, label+": "+value)
	}
}

func validateJSONFields(contents []byte, allowed ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("expected JSON object")
	}
	valid := map[string]bool{}
	for _, name := range allowed {
		valid[name] = true
	}
	for name := range fields {
		if !valid[name] {
			return fmt.Errorf("unknown JSON field %q", name)
		}
	}
	return nil
}

func validateMCPServer(contents []byte, name, serverType string) error {
	var config struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		return err
	}
	server := config.Servers[name]
	switch serverType {
	case "stdio":
		if err := validateJSONFields(server, "type", "command", "args", "env", "cwd"); err != nil {
			return err
		}
		var parsed struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
			CWD  string            `json:"cwd"`
		}
		if err := json.Unmarshal(server, &parsed); err != nil {
			return err
		}
		for key := range parsed.Env {
			if key == "PLUGIN_ROOT" || key == "PLUGIN_DATA" {
				return fmt.Errorf("reserved env name %q", key)
			}
		}
		if parsed.CWD != "" {
			if parsed.CWD == "./" || parsed.CWD == "${PLUGIN_ROOT}" || parsed.CWD == "${PLUGIN_DATA}" {
				return nil
			}
			if !strings.HasPrefix(parsed.CWD, "./") && !strings.HasPrefix(parsed.CWD, "${PLUGIN_ROOT}/") && !strings.HasPrefix(parsed.CWD, "${PLUGIN_DATA}/") {
				return errors.New("cwd must be plugin-relative")
			}
			local := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(parsed.CWD, "${PLUGIN_ROOT}/"), "${PLUGIN_DATA}/"), "./")
			if !safePackageComponent(local) {
				return errors.New("cwd escapes plugin root")
			}
		}
	case "streamable-http", "sse":
		if err := validateJSONFields(server, "type", "url", "headers"); err != nil {
			return err
		}
		var parsed struct {
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(server, &parsed); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported transport type %q", serverType)
	}
	return nil
}

func safePackageComponent(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.Contains(path, "\\") && filepath.Clean(path) != ".." && !strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator))
}

func withinPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
