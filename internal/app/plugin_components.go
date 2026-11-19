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
)

// A package is read without executing hooks, agents or MCP commands. The same
// reader feeds project generation and prepared native variants.
type pluginComponents struct {
	Skills   []skill
	Agents   []agent
	MCP      mcpConfig
	Omitted  []string
	Metadata map[string]any
}

func readPluginComponents(root string) (pluginComponents, error) {
	if _, err := readPackageFiles(root); err != nil {
		return pluginComponents{}, err
	}
	result := pluginComponents{MCP: mcpConfig{Servers: map[string]mcpServer{}}, Metadata: map[string]any{}}
	manifest := filepath.Join(root, ".claude-plugin/plugin.json")
	data, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join(root, "plugin.json"))
	}
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &result.Metadata); err != nil {
			return result, fmt.Errorf("plugin manifest: %w", err)
		}
	}
	if selection, ok := result.Metadata["skills"]; ok {
		paths, err := pluginComponentPaths(selection)
		if err != nil {
			return result, fmt.Errorf("skills: %w", err)
		}
		seen := map[string]bool{}
		for _, rel := range paths {
			path, err := containedPluginPath(root, rel)
			if err != nil {
				return result, err
			}
			// Claude allows a skill directory or a directory containing skills.
			var found []skill
			if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err == nil {
				item, err := readPluginSkill(path)
				if err != nil {
					return result, err
				}
				found = []skill{item}
			} else {
				found, err = readPluginSkillChildren(path)
				if err != nil {
					return result, err
				}
			}
			for _, item := range found {
				if seen[item.Name] {
					return result, fmt.Errorf("duplicate plugin skill %q", item.Name)
				}
				seen[item.Name] = true
				result.Skills = append(result.Skills, item)
			}
		}
	} else {
		result.Skills, err = readPluginSkillChildren(filepath.Join(root, "skills"))
		if err != nil {
			return result, err
		}
	}
	result.Agents, err = loadAgents(filepath.Join(root, "agents"))
	if err != nil {
		return result, err
	}
	if _, ok := result.Metadata["agents"]; ok {
		result.Omitted = append(result.Omitted, "custom agents paths (only agents/ is adapted)")
	}
	// A manifest can hold inline MCP servers or paths to JSON configurations.
	readMCP := map[string]bool{}
	if value, ok := result.Metadata["mcpServers"]; ok {
		if servers, inline := value.(map[string]any); inline {
			data, _ := json.Marshal(map[string]any{"mcpServers": servers})
			if err = mergePluginMCP(root, data, &result.MCP, &result.Omitted); err != nil {
				return result, err
			}
		} else {
			paths, err := pluginComponentPaths(value)
			if err != nil {
				return result, err
			}
			for _, rel := range paths {
				path, err := containedPluginPath(root, rel)
				if err != nil {
					return result, err
				}
				readMCP[filepath.Clean(path)] = true
				data, err := os.ReadFile(path)
				if err != nil {
					return result, err
				}
				if err = mergePluginMCP(root, data, &result.MCP, &result.Omitted); err != nil {
					return result, err
				}
			}
		}
	}
	for _, name := range []string{".mcp.json", "mcp.json"} {
		if readMCP[filepath.Clean(filepath.Join(root, name))] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		if err = mergePluginMCP(root, data, &result.MCP, &result.Omitted); err != nil {
			return result, err
		}
	}
	for _, component := range []string{"hooks", "commands", "outputStyles", "lspServers"} {
		_, declared := result.Metadata[component]
		_, statErr := os.Stat(filepath.Join(root, component))
		if declared || statErr == nil {
			result.Omitted = append(result.Omitted, component)
		}
	}
	if len(result.Agents) > 0 {
		result.Omitted = append(result.Omitted, "native agents (referenced instructions are bundled with skills)")
	}
	known := map[string]bool{"$schema": true, "name": true, "version": true, "description": true, "author": true, "homepage": true, "repository": true, "license": true, "keywords": true, "skills": true, "agents": true, "mcpServers": true, "hooks": true, "commands": true, "outputStyles": true, "lspServers": true}
	for _, key := range sortedStringMapKeys(result.Metadata) {
		if !known[key] {
			result.Omitted = append(result.Omitted, "manifest field "+key)
		}
	}
	for _, component := range []string{".codex-plugin", ".app.json"} {
		if _, err := os.Stat(filepath.Join(root, component)); err == nil {
			result.Omitted = append(result.Omitted, component)
		}
	}
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].Name < result.Skills[j].Name })
	sort.Strings(result.Omitted)
	return result, nil
}

func pluginComponentPaths(value any) ([]string, error) {
	if path, ok := value.(string); ok {
		return []string{path}, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("component must be a path or array of paths")
	}
	paths := make([]string, 0, len(values))
	for _, value := range values {
		path, ok := value.(string)
		if !ok {
			return nil, errors.New("component path must be a string")
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func containedPluginPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("invalid plugin component path %q", rel)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil || !withinPath(absRoot, absPath) {
		return "", fmt.Errorf("plugin component %q escapes package", rel)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !withinPath(realRoot, realPath) {
		return "", fmt.Errorf("plugin component %q escapes package through symlink", rel)
	}
	return path, nil
}

func readPluginSkillChildren(root string) ([]skill, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []skill
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item, err := readPluginSkill(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		skills = append(skills, item)
	}
	return skills, nil
}

func readPluginSkill(root string) (skill, error) {
	name := filepath.Base(root)
	files, err := readPackageFiles(root)
	if err != nil {
		return skill{}, err
	}
	if err = validateInventorySkill(files["SKILL.md"].Content, name); err != nil {
		return skill{}, fmt.Errorf("plugin skill %s: %w", root, err)
	}
	result := skill{Name: name, Files: map[string][]byte{}, Executable: map[string]bool{}}
	for rel, file := range files {
		result.Files[rel] = file.Content
		result.Executable[rel] = file.Executable
	}
	return result, nil
}

// All package reads reject symlinks, including links in the lock's cache.
func readPackageFiles(root string) (vendoredSkill, error) { return readPackageTree(root, false) }

// Git checkouts may contain file aliases such as AGENTS.md -> CLAUDE.md.
// Materialize only regular files whose resolved target stays in the checkout.
func readPackageTree(root string, internalLinks bool) (vendoredSkill, error) {
	files := vendoredSkill{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			if !internalLinks || entry.Type()&os.ModeSymlink == 0 {
				return fmt.Errorf("unsupported package file %s (symlinks are not allowed)", path)
			}
			realRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				return err
			}
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			if !withinPath(realRoot, real) {
				return fmt.Errorf("package symlink %s escapes checkout", path)
			}
			target, err := os.Stat(real)
			if err != nil {
				return err
			}
			if !target.Mode().IsRegular() {
				return fmt.Errorf("package symlink %s must point to a regular file", path)
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = vendoredFile{Content: data, Executable: info.Mode().Perm()&0111 != 0}
		return nil
	})
	return files, err
}

func mergePluginMCP(root string, data []byte, config *mcpConfig, omitted *[]string) error {
	// Expand only the package-root placeholder; secrets remain environment references.
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	var doc struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err = json.Unmarshal(data, &doc); err != nil || doc.Servers == nil {
		return errors.New("plugin MCP configuration needs mcpServers")
	}
	for _, name := range sortedStringMapKeys(doc.Servers) {
		if _, exists := config.Servers[name]; exists {
			return fmt.Errorf("duplicate plugin MCP server %q", name)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(doc.Servers[name], &fields); err != nil {
			return err
		}
		unsupported := []string{}
		for _, key := range sortedStringMapKeys(fields) {
			switch key {
			case "type", "command", "args", "env", "url", "headers", "bearer_token_env_var":
			default:
				unsupported = append(unsupported, key)
			}
		}
		if len(unsupported) > 0 {
			*omitted = append(*omitted, fmt.Sprintf("MCP %s: unsupported fields %s", name, strings.Join(unsupported, ", ")))
			continue
		}
		var server struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Bearer  string            `json:"bearer_token_env_var"`
		}
		if err = json.Unmarshal(doc.Servers[name], &server); err != nil {
			return err
		}
		if server.Type != "" && server.Type != "stdio" && server.Type != "http" && server.Type != "streamable-http" {
			*omitted = append(*omitted, fmt.Sprintf("MCP %s: unsupported transport %s", name, server.Type))
			continue
		}
		replace := func(s string) string { return strings.ReplaceAll(s, "${CLAUDE_PLUGIN_ROOT}", absolute) }
		server.Command = replace(server.Command)
		if strings.HasPrefix(server.Command, "./") {
			path, err := containedPluginPath(root, server.Command)
			if err != nil {
				return err
			}
			server.Command, err = filepath.Abs(path)
			if err != nil {
				return err
			}
		}
		if filepath.IsAbs(server.Command) && !withinPath(absolute, server.Command) {
			*omitted = append(*omitted, fmt.Sprintf("MCP %s: absolute executable outside package has no portable adapter", name))
			continue
		}
		reserved := false
		for key := range server.Env {
			if key == "PLUGIN_ROOT" || key == "PLUGIN_DATA" {
				reserved = true
			}
		}
		if reserved {
			*omitted = append(*omitted, fmt.Sprintf("MCP %s: reserved plugin environment variable", name))
			continue
		}
		server.URL = replace(server.URL)
		for i := range server.Args {
			server.Args[i] = replace(server.Args[i])
		}
		for key, value := range server.Env {
			server.Env[key] = replace(value)
		}
		result := mcpServer{Command: server.Command, Args: server.Args, Env: server.Env, URL: server.URL, Headers: server.Headers, BearerTokenEnvVar: server.Bearer}
		if err = validateMCPServerSource(result); err != nil {
			*omitted = append(*omitted, fmt.Sprintf("MCP %s: %s", name, err))
			continue
		}
		config.Servers[name] = result
	}
	return nil
}
