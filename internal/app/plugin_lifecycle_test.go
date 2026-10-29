package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func TestPluginStatusScopesClaudeInstallationToProject(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  kit@tools: true\n")
	home := t.TempDir()
	t.Setenv("HOME", home)
	registry, _ := json.Marshal(map[string]any{"plugins": map[string]any{"kit@tools": []any{map[string]string{"scope": "project", "projectPath": other, "version": "1.0"}}}})
	writeFile(t, home, ".claude/plugins/installed_plugins.json", string(registry))

	out, err := app.Run(project, []string{"plugins", "status", project})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Plugins []struct {
			Name      string `json:"name"`
			Installed bool   `json:"claude_installed"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Plugins) != 1 || doc.Plugins[0].Installed {
		t.Fatalf("other project's installation leaked into status: %s, %v", out, err)
	}
	registry, _ = json.Marshal(map[string]any{"plugins": map[string]any{"kit@tools": []any{map[string]string{"scope": "project", "projectPath": project, "version": "1.0"}}}})
	writeFile(t, home, ".claude/plugins/installed_plugins.json", string(registry))
	out, err = app.Run(project, []string{"plugins", "status", project})
	if err != nil || !strings.Contains(out, `"claude_installed":true`) || !strings.Contains(out, `"installed_version":"1.0"`) {
		t.Fatalf("current project's installation missing: %s, %v", out, err)
	}
}

func TestPluginUpdatesComparesCachedMarketplaceVersion(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  kit@tools: true\n")
	home := t.TempDir()
	t.Setenv("HOME", home)
	registry, _ := json.Marshal(map[string]any{"plugins": map[string]any{"kit@tools": []any{map[string]string{"scope": "project", "projectPath": project, "version": "1.0"}}}})
	writeFile(t, home, ".claude/plugins/installed_plugins.json", string(registry))
	writeFile(t, home, ".claude/plugins/marketplaces/tools/.claude-plugin/marketplace.json", `{"plugins":[{"name":"kit","source":"./"}]}`)
	writeFile(t, home, ".claude/plugins/marketplaces/tools/.claude-plugin/plugin.json", `{"name":"kit","version":"2.0"}`)

	out, err := app.Run(project, []string{"plugins", "updates", project})
	if err != nil || !strings.Contains(out, `"available_version":"2.0"`) || !strings.Contains(out, `"update_state":"update_available"`) {
		t.Fatalf("cached update not reported: %s, %v", out, err)
	}
}

func TestPluginUpdatesRefreshesDeclaredRemoteMarketplace(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  kit@tools: true\n")
	t.Setenv("HOME", t.TempDir())
	log := filepath.Join(t.TempDir(), "claude.log")
	fakeClaudeOnPath(t, "printf '%s\\n' \"$*\" >> '"+log+"'\nexit 0")
	if _, err := app.Run(project, []string{"plugins", "updates", project, "--refresh"}); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "plugin marketplace update tools") {
		t.Fatalf("marketplace refresh missing: %s, %v", commands, err)
	}
}

func TestPluginStatusIncludesNonClaudeVariantWithoutClaimingInstallation(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  toolkit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/toolkit\n")
	writeFile(t, project, ".ai/packages/toolkit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"toolkit","version":"1.0","description":"Toolkit"}`)
	t.Setenv("HOME", t.TempDir())
	out, err := app.Run(project, []string{"plugins", "status", project})
	if err != nil || !strings.Contains(out, `"declared_targets":["kiro"]`) || !strings.Contains(out, `"claude_installed":false`) || !strings.Contains(out, `"version":"1.0"`) {
		t.Fatalf("target variant missing from status: %s, %v", out, err)
	}
}

func TestPluginAddUpdateRemoveClaudePreservesOtherConfig(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "env:\n  MODE: off\n")
	home := t.TempDir()
	t.Setenv("HOME", home)
	log := filepath.Join(t.TempDir(), "claude.log")
	fakeClaudeOnPath(t, "printf '%s\\n' \"$*\" >> '"+log+"'\nexit 0")

	if _, err := app.Run(project, []string{"plugins", "add", project, "kit@tools", "--target", "claude", "--repo", "org/tools"}); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil || !strings.Contains(string(config), "MODE: off") || !strings.Contains(string(config), "kit@tools: true") {
		t.Fatalf("add changed unrelated YAML: %s, %v", config, err)
	}
	settings, err := os.ReadFile(filepath.Join(project, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), "kit@tools") {
		t.Fatalf("Claude settings not rendered: %s, %v", settings, err)
	}
	registry, _ := json.Marshal(map[string]any{"plugins": map[string]any{"kit@tools": []any{map[string]string{"scope": "project", "projectPath": project, "version": "1.0"}}}})
	writeFile(t, home, ".claude/plugins/installed_plugins.json", string(registry))
	if _, err := app.Run(project, []string{"plugins", "update", project, "kit@tools", "--target", "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(project, []string{"plugins", "remove", project, "kit@tools", "--target", "claude"}); err != nil {
		t.Fatal(err)
	}
	config, err = os.ReadFile(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil || strings.Contains(string(config), "kit@tools") || !strings.Contains(string(config), "MODE: off") {
		t.Fatalf("remove changed unrelated YAML: %s, %v", config, err)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "plugin install kit@tools --scope project --json --yes") || !strings.Contains(string(commands), "plugin update kit@tools --scope project --json") || !strings.Contains(string(commands), "plugin uninstall kit@tools --scope project --json") {
		t.Fatalf("native lifecycle commands: %s, %v", commands, err)
	}
}
