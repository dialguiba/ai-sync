package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

type applyDoc struct {
	Target  string `json:"target"`
	Plugins []struct {
		Name         string   `json:"name"`
		State        string   `json:"state"`
		Status       string   `json:"status"`
		Error        string   `json:"error"`
		Command      []string `json:"command"`
		Instructions []string `json:"instructions"`
	} `json:"plugins"`
}

func TestPluginApplyRejectsStaleFingerprintBeforeInstaller(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	preview := getPreview(t, project, "kiro")
	writeFile(t, project, ".ai/packages/kit/README.md", "changed")
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	writeFile(t, bin, "kiro-cli", "#!/bin/sh\ntouch '"+marker+"'\n")
	if err := os.Chmod(filepath.Join(bin, "kiro-cli"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := app.Run(t.TempDir(), []string{"plugins", "apply", project, "--target", "kiro", "--fingerprint", preview.Fingerprint, "--all"})
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("expected stale fingerprint: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer ran after stale preview")
	}
}

func fakeInstaller(t *testing.T, name string) (string, string) {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	writeFile(t, bin, name, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+marker+"'\n")
	if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return bin, marker
}

func runApply(t *testing.T, project, target, fingerprint string, extra ...string) (applyDoc, string, error) {
	t.Helper()
	args := append([]string{"plugins", "apply", project, "--target", target}, extra...)
	args = append(args, "--fingerprint", fingerprint)
	out, err := app.Run(t.TempDir(), args)
	var doc applyDoc
	if err == nil {
		if decodeErr := json.Unmarshal([]byte(out), &doc); decodeErr != nil {
			t.Fatalf("decode apply: %v, %s", decodeErr, out)
		}
	}
	return doc, out, err
}

func TestPluginApplyInstallsCompatibleKiroPower(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-kit","version":"1","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	writeFile(t, project, ".ai/packages/kit/skills/search/SKILL.md", "---\nname: search\ndescription: Search\n---\nSearch\n")
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"docs":{"type":"streamable-http","url":"https://example.com/mcp"}}}`)
	preview := getPreview(t, project, "kiro")
	_, marker := fakeInstaller(t, "kiro-cli")
	doc, out, err := runApply(t, project, "kiro", preview.Fingerprint, "--all")
	if err != nil {
		t.Fatalf("apply: %v, %s", err, out)
	}
	if len(doc.Plugins) != 1 || doc.Plugins[0].Name != "kit" || doc.Plugins[0].Status != "installed" || doc.Plugins[0].State != "compatible" {
		t.Fatalf("unexpected apply result: %+v", doc)
	}
	installed, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("installer not called: %v", err)
	}
	want := "powers\ninstall\n" + filepath.Join(project, ".ai/packages/kit") + "\n"
	if string(installed) != want {
		t.Fatalf("installer arguments: %q, want %q", installed, want)
	}
}

func TestPluginApplyReportsCodexManualStepsWithoutInstalling(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-kit","version":"1","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	preview := getPreview(t, project, "codex")
	bin, marker := fakeInstaller(t, "codex")
	doc, out, err := runApply(t, project, "codex", preview.Fingerprint, "--all")
	if err != nil {
		t.Fatalf("apply: %v, %s", err, out)
	}
	if len(doc.Plugins) != 1 || doc.Plugins[0].Status != "manual" || len(doc.Plugins[0].Instructions) == 0 || len(doc.Plugins[0].Command) != 0 {
		t.Fatalf("Codex apply did not report manual steps: %+v", doc)
	}
	if !strings.Contains(strings.Join(doc.Plugins[0].Instructions, "\n"), "codex plugin add") {
		t.Fatalf("manual steps are not concrete: %+v", doc.Plugins[0].Instructions)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Codex apply executed a command")
	}
	if _, err := os.Stat(filepath.Join(bin, "codex")); err != nil {
		t.Fatalf("fake codex binary missing: %v", err)
	}
}

func TestPluginApplyBlocksNativeCollisionBeforeInstalling(t *testing.T) {
	project := previewFixture(t, "plugins:\n  alpha:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/alpha\n  beta:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/beta\n")
	for _, name := range []string{"alpha", "beta"} {
		writeFile(t, project, ".ai/packages/"+name+"/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"same","version":"1","description":"Plugin","author":{"name":"Team"},"keywords":["plugin"]}`)
	}
	preview := getPreview(t, project, "kiro")
	_, marker := fakeInstaller(t, "kiro-cli")
	_, _, err := runApply(t, project, "kiro", preview.Fingerprint, "--all")
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("collision did not block the apply: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer ran while a plugin was blocked")
	}
}

func TestPluginApplyReportsUninstallableVariantsWithoutInstalling(t *testing.T) {
	project := previewFixture(t, "plugins:\n  claude-only:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/claude\n  remote:\n    enabled: true\n    targets:\n      codex:\n        git: https://example.com/plugin.git\n        ref: 0123456789012345678901234567890123456789\n")
	writeFile(t, project, ".ai/packages/claude/.claude-plugin/plugin.json", `{"name":"claude-only"}`)
	preview := getPreview(t, project, "codex")
	_, marker := fakeInstaller(t, "codex")
	doc, out, err := runApply(t, project, "codex", preview.Fingerprint, "--all")
	if err != nil {
		t.Fatalf("reported variants should not fail the apply: %v, %s", err, out)
	}
	reported := map[string]struct{ state, status, reason string }{}
	for _, plugin := range doc.Plugins {
		reported[plugin.Name] = struct{ state, status, reason string }{plugin.State, plugin.Status, plugin.Error}
	}
	incompatible, ok := reported["claude-only"]
	if !ok || incompatible.status != "incompatible" || !strings.Contains(incompatible.reason, ".claude-plugin") {
		t.Fatalf("incompatible variant lost its reason: %+v", doc)
	}
	remote, ok := reported["remote"]
	if !ok || remote.status != "unverified" || !strings.Contains(remote.reason, "Git package") {
		t.Fatalf("unverified variant lost its reason: %+v", doc)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer ran for uninstallable variants")
	}
}

func TestPluginApplyRequiresReviewedFingerprintAndSelection(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	preview := getPreview(t, project, "kiro")
	_, marker := fakeInstaller(t, "kiro-cli")
	if _, err := app.Run(t.TempDir(), []string{"plugins", "apply", project, "--target", "kiro", "--all"}); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("missing fingerprint accepted: %v", err)
	}
	if _, _, err := runApply(t, project, "kiro", preview.Fingerprint); err == nil || !strings.Contains(err.Error(), "--plugin") {
		t.Fatalf("empty selection accepted: %v", err)
	}
	if _, _, err := runApply(t, project, "kiro", preview.Fingerprint, "--plugin", "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown plugin accepted: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("installer ran without a valid selection")
	}
}
