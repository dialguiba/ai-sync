package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pluginPreview struct {
	Target      string                `json:"target"`
	Fingerprint string                `json:"fingerprint"`
	Plugins     []previewPlugin       `json:"plugins"`
	Diagnostics []inventoryDiagnostic `json:"diagnostics"`
}
type previewPlugin struct {
	Name           string        `json:"name"`
	Enabled        bool          `json:"enabled"`
	State          string        `json:"state"`
	NativeIdentity string        `json:"native_identity,omitempty"`
	Package        pluginTarget  `json:"package"`
	Manifest       string        `json:"manifest,omitempty"`
	Version        string        `json:"version,omitempty"`
	Skills         []string      `json:"skills"`
	MCPServers     []string      `json:"mcp_servers"`
	Omitted        []string      `json:"omitted_components"`
	Missing        []string      `json:"missing_requirements"`
	Action         previewAction `json:"action"`
	Conflicts      []string      `json:"conflicts"`
}
type previewAction struct {
	Kind         string   `json:"kind"`
	Command      []string `json:"command,omitempty"`
	Instructions []string `json:"instructions,omitempty"`
}

func previewPlugins(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync plugins preview <project-dir> --target codex|kiro\n", nil
	}
	if len(args) != 3 || args[1] != "--target" || (args[2] != "codex" && args[2] != "kiro") || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return "", errors.New("usage: ai-sync plugins preview <project-dir> --target codex|kiro")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	raw, inventoryErr := inventoryProject(workdir, []string{project})
	var inventory inventoryResult
	if err := json.Unmarshal([]byte(raw), &inventory); err != nil {
		return "", fmt.Errorf("decode inventory: %w", err)
	}
	result := pluginPreview{Target: args[2], Plugins: []previewPlugin{}, Diagnostics: inventory.Diagnostics}
	for _, resource := range inventory.Resources {
		if resource.Type != "plugin" {
			continue
		}
		for _, variant := range resource.Variants {
			if variant.Target != result.Target {
				continue
			}
			p := previewPlugin{Name: resource.Name, Enabled: resource.Enabled != nil && *resource.Enabled, State: variant.Status, NativeIdentity: variant.PackageName, Package: variant.Source, Manifest: variant.Manifest, Version: variant.Version, Skills: variant.Skills, MCPServers: variant.MCPServers, Omitted: variant.Unsupported, Missing: variant.Requirements, Conflicts: []string{}, Action: previewAction{Kind: "none"}}
			if p.Skills == nil {
				p.Skills = []string{}
			}
			if p.MCPServers == nil {
				p.MCPServers = []string{}
			}
			if p.Omitted == nil {
				p.Omitted = []string{}
			}
			if p.Missing == nil {
				p.Missing = []string{}
			}
			if p.Enabled && p.State == "compatible" {
				local := filepath.Join(project, filepath.FromSlash(variant.Source.Path))
				if result.Target == "kiro" {
					p.Action = previewAction{Kind: "command", Command: []string{"kiro-cli", "powers", "install", local}}
				} else {
					entry, _ := json.Marshal(map[string]any{
						"name":     variant.PackageName,
						"source":   map[string]string{"source": "local", "path": "./" + variant.Source.Path},
						"policy":   map[string]string{"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
						"category": "Productivity",
					})
					p.Action = previewAction{Kind: "manual", Instructions: []string{
						"Add this entry under plugins in " + filepath.Join(project, ".agents/plugins/marketplace.json") + " (create the marketplace with a top-level name if needed): " + string(entry),
						"Restart Codex, then run codex plugin add " + variant.PackageName + "@<marketplace-name>.",
					}}
				}
			}
			if p.Enabled && p.State == "partial" {
				p.Action = previewAction{Kind: "pending", Instructions: []string{"A target-specific package with omitted components removed must be prepared and validated before installation."}}
			}
			result.Plugins = append(result.Plugins, p)
		}
	}
	// Native package identities and package paths are exclusive. Skill and MCP names
	// remain scoped inside each plugin and are intentionally absent from this check.
	seen := map[string]int{}
	for i := range result.Plugins {
		p := &result.Plugins[i]
		if !p.Enabled {
			continue
		}
		for _, key := range []string{"name:" + p.NativeIdentity, "path:" + p.Package.Path} {
			if strings.HasSuffix(key, ":") {
				continue
			}
			if owner, ok := seen[key]; ok && result.Plugins[owner].Name != p.Name {
				other := &result.Plugins[owner]
				p.Conflicts = append(p.Conflicts, fmt.Sprintf("%s also belongs to plugin %q", key, other.Name))
				other.Conflicts = append(other.Conflicts, fmt.Sprintf("%s also belongs to plugin %q", key, p.Name))
			} else {
				seen[key] = i
			}
		}
	}
	for i := range result.Plugins {
		p := &result.Plugins[i]
		sort.Strings(p.Conflicts)
		if len(p.Conflicts) > 0 {
			p.Action = previewAction{Kind: "blocked", Instructions: []string{"Choose one owner or a distinct native identity and package path, then preview again."}}
		}
	}
	fingerprint, err := previewFingerprint(project, raw)
	if err != nil {
		return "", err
	}
	result.Fingerprint = fingerprint
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	if inventoryErr != nil && inventory.Status != "invalid" {
		return string(encoded) + "\n", inventoryErr
	}
	return string(encoded) + "\n", nil
}

func previewFingerprint(project, inventoryJSON string) (string, error) {
	hash := sha256.New()
	hash.Write([]byte("ai-sync plugin preview v1\x00"))
	hash.Write([]byte(inventoryJSON))
	root := filepath.Join(project, ".ai")
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, _ := filepath.Rel(project, path)
		hash.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			hash.Write([]byte("symlink:" + target))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
