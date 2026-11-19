package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func preparePluginPackage(destination, root, name string, components pluginComponents) error {
	files := vendoredSkill{}
	metadata := map[string]any{"$schema": pluginSchema, "name": name, "version": "0.0.0", "description": "Prepared project plugin " + name, "author": map[string]string{"name": "ai-sync"}, "keywords": []string{"plugin"}}
	for _, key := range []string{"version", "description", "author", "keywords", "homepage", "repository", "license"} {
		if value, ok := components.Metadata[key]; ok {
			metadata[key] = value
		}
	}
	data, _ := json.MarshalIndent(metadata, "", "  ")
	files["plugin.json"] = vendoredFile{Content: data}
	raw, err := readPackageFiles(root)
	if err != nil {
		return err
	}
	for rel, file := range raw {
		files["assets/package/"+rel] = file
	}
	for _, item := range components.Skills {
		item = withReferencedAgents(name, item, components.Agents)
		for rel, content := range item.Files {
			content = []byte(strings.ReplaceAll(string(content), "${CLAUDE_PLUGIN_ROOT}", "${PLUGIN_ROOT}/assets/package"))
			files["skills/"+item.Name+"/"+rel] = vendoredFile{Content: content, Executable: item.Executable[rel]}
		}
	}
	absolute, _ := filepath.Abs(root)
	servers := mcpServersJSON(components.MCP, false)
	for serverName, value := range servers {
		entry := value.(map[string]any)
		if command, ok := entry["command"].(string); ok && filepath.IsAbs(command) && withinPath(absolute, command) {
			rel, _ := filepath.Rel(absolute, command)
			entry["command"] = "./assets/package/" + filepath.ToSlash(rel)
		}
		if entry["url"] != nil {
			entry["type"] = "streamable-http"
		} else {
			entry["type"] = "stdio"
		}
		servers[serverName] = entry
	}
	data, _ = json.Marshal(map[string]any{"$schema": mcpSchema, "mcpServers": servers})
	// Marshal the replacement too: Windows paths contain JSON escapes.
	from, _ := json.Marshal(absolute)
	to, _ := json.Marshal("${PLUGIN_ROOT}/assets/package")
	data = []byte(strings.ReplaceAll(string(data), string(from[1:len(from)-1]), string(to[1:len(to)-1])))
	if len(servers) > 0 {
		files["mcp.json"] = vendoredFile{Content: data}
	}
	return writeVendoredSkill(destination, files)
}

func validatePreparedPluginPackages(project, stage string) error {
	entries, err := os.ReadDir(filepath.Join(stage, "prepared"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		relative, err := filepath.Rel(project, filepath.Join(stage, "prepared", entry.Name()))
		if err != nil {
			return err
		}
		for _, target := range []string{"codex", "kiro"} {
			variant, diagnostics := inspectPluginTarget(project, entry.Name(), target, pluginTarget{Path: filepath.ToSlash(relative)}, map[string]bool{})
			if variant.Status == "invalid" || variant.Status == "incompatible" {
				return fmt.Errorf("prepared %s package %q is %s: %v %v", target, entry.Name(), variant.Status, diagnostics, variant.Requirements)
			}
		}
	}
	return nil
}
