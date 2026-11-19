package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadPluginProjectMCP(workdir string, config pluginConfig, canonical mcpConfig) (mcpConfig, error) {
	result := mcpConfig{Servers: map[string]mcpServer{}}
	for _, identity := range sortedStringMapKeys(config.Plugins) {
		name, market, _ := strings.Cut(identity, "@")
		source := config.Marketplaces[market]
		if !projectPluginEnabled(config, identity) || resolveMarketplaceSource(source) != "path" {
			continue
		}
		root, err := localPluginDir(absoluteMarketplacePath(source.Path, workdir), name)
		if err == errNoMarketplaceManifest {
			continue
		}
		if err != nil {
			return result, err
		}
		components, err := readPluginComponents(root)
		if err != nil {
			return result, err
		}
		for serverName, server := range components.MCP.Servers {
			qualified := name + "-" + serverName
			if _, ok := canonical.Servers[qualified]; ok {
				return result, fmt.Errorf("plugin %s MCP %q collides with canonical server", identity, qualified)
			}
			if _, ok := result.Servers[qualified]; ok {
				return result, fmt.Errorf("plugin %s MCP %q collides with another plugin", identity, qualified)
			}
			if server.Command != "" && len(config.Env) > 0 {
				env := map[string]string{}
				for key, value := range config.Env {
					env[key] = value
				}
				for key, value := range server.Env {
					env[key] = value
				}
				server.Env = env
			}
			result.Servers[qualified] = server
		}
	}
	return result, nil
}

func pluginProjectionNotes(workdir, target string) ([]string, error) {
	if target == "claude" {
		return nil, nil
	}
	config, err := loadResolvedPlugins(workdir)
	if err != nil {
		return nil, err
	}
	var notes []string
	for _, identity := range sortedStringMapKeys(config.Plugins) {
		name, market, _ := strings.Cut(identity, "@")
		source := config.Marketplaces[market]
		if !projectPluginEnabled(config, identity) {
			continue
		}
		if resolveMarketplaceSource(source) != "path" {
			notes = append(notes, fmt.Sprintf("pending %s for Codex/Kiro: resolve its package with ai-sync plugins sync . (repo marketplaces); URL marketplaces need a local path", identity))
			continue
		}
		root, err := localPluginDir(absoluteMarketplacePath(source.Path, workdir), name)
		if err == errNoMarketplaceManifest {
			continue
		}
		if err != nil {
			return nil, err
		}
		components, err := readPluginComponents(root)
		if err != nil {
			return nil, err
		}
		if len(components.Omitted) > 0 {
			notes = append(notes, fmt.Sprintf("warning %s for Codex/Kiro: not propagated as native components: %s; retained in the Claude package", identity, strings.Join(components.Omitted, ", ")))
		}
	}
	return notes, nil
}

// Once resolved, legacy marketplace entries expose prepared native variants in
// the same preview/apply workflow as explicit per-target packages.
func resolvedMarketplaceVariants(project, identity string, marketplace marketplaceSource, known map[string]bool) ([]inventoryPluginVariant, []inventoryDiagnostic) {
	lock, err := readPluginLock(project)
	if err != nil {
		return nil, []inventoryDiagnostic{{Path: ".ai/" + pluginLockFile, Message: err.Error()}}
	}
	name, market, _ := strings.Cut(identity, "@")
	pin, ok := lock.Sources["marketplace:"+market]
	if !ok {
		return nil, nil
	}
	git, err := marketplaceGit(marketplace)
	if err != nil || git != pin.Git {
		return nil, nil
	}
	if err = verifyPluginPackage(project, pin); err != nil {
		return nil, []inventoryDiagnostic{{Path: ".ai/" + pluginLockFile, Unit: identity, Message: err.Error()}}
	}
	packagePath := filepath.ToSlash(filepath.Join(pluginPackagePath(pin), "prepared", name))
	if _, err = os.Stat(filepath.Join(project, packagePath)); err != nil {
		return nil, []inventoryDiagnostic{{Path: packagePath, Unit: identity, Message: "plugin package not prepared; run ai-sync plugins sync ."}}
	}
	root, err := localPluginDir(filepath.Join(project, pluginPackagePath(pin), "source"), name)
	if err != nil {
		return nil, []inventoryDiagnostic{{Path: packagePath, Unit: identity, Message: err.Error()}}
	}
	components, err := readPluginComponents(root)
	if err != nil {
		return nil, []inventoryDiagnostic{{Path: packagePath, Unit: identity, Message: err.Error()}}
	}
	rawPath := filepath.ToSlash(filepath.Join(pluginPackagePath(pin), "source", mustRelative(filepath.Join(project, pluginPackagePath(pin), "source"), root)))
	version, _ := components.Metadata["version"].(string)
	manifest := filepath.ToSlash(filepath.Join(rawPath, ".claude-plugin/plugin.json"))
	claude := inventoryPluginVariant{Target: "claude", Status: "compatible", Source: pluginTarget{Install: identity, Path: rawPath, Git: pin.Git, Ref: pin.Ref}, Manifest: manifest, PackageName: name, Version: version, Skills: []string{}, MCPServers: []string{}, SkillSources: map[string]string{}, MCPServerSources: map[string]string{}, Unsupported: []string{}, Requirements: []string{}}
	for _, item := range components.Skills {
		claude.Skills = append(claude.Skills, item.Name)
		claude.SkillSources[item.Name] = manifest
	}
	for _, server := range sortedServerNames(components.MCP.Servers) {
		claude.MCPServers = append(claude.MCPServers, server)
		claude.MCPServerSources[server] = manifest
	}
	variants := []inventoryPluginVariant{claude}
	var diagnostics []inventoryDiagnostic
	for _, target := range []string{"codex", "kiro"} {
		variant, ds := inspectPluginTarget(project, identity, target, pluginTarget{Path: packagePath, Git: pin.Git, Ref: pin.Ref}, known)
		if len(components.Omitted) > 0 && variant.Status == "compatible" {
			variant.Status = "partial"
			variant.Unsupported = append(variant.Unsupported, components.Omitted...)
		}
		variants = append(variants, variant)
		diagnostics = append(diagnostics, ds...)
	}
	// The retained raw checkout is owned by the plugin, not a set of loose skills.
	files, _ := readPackageFiles(filepath.Join(project, pluginPackagePath(pin), "source"))
	for rel := range files {
		known[filepath.ToSlash(filepath.Join(pluginPackagePath(pin), "source", rel))] = true
	}
	known[".ai/"+pluginLockFile] = true
	return variants, diagnostics
}

// Boolean marketplace entries use project projection. Structured declarations
// retain their explicit target selection and native preview/apply workflow.
func projectPluginEnabled(config pluginConfig, identity string) bool {
	if !config.Plugins[identity] {
		return false
	}
	for _, declaration := range config.Portable {
		if declaration.Targets["claude"].Install == identity {
			return false
		}
	}
	return true
}
