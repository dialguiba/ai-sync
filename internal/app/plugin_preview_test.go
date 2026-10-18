package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

type previewDoc struct {
	Target      string `json:"target"`
	Fingerprint string `json:"fingerprint"`
	Plugins     []struct {
		Name           string   `json:"name"`
		State          string   `json:"state"`
		NativeIdentity string   `json:"native_identity"`
		Skills         []string `json:"skills"`
		MCPServers     []string `json:"mcp_servers"`
		Omitted        []string `json:"omitted_components"`
		Missing        []string `json:"missing_requirements"`
		Action         struct {
			Kind         string   `json:"kind"`
			Command      []string `json:"command"`
			Instructions []string `json:"instructions"`
		} `json:"action"`
		Conflicts []string `json:"conflicts"`
	} `json:"plugins"`
}

func previewFixture(t *testing.T, yaml string) string {
	t.Helper()
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", yaml)
	return project
}
func getPreview(t *testing.T, project, target string) previewDoc {
	t.Helper()
	out, err := app.Run(t.TempDir(), []string{"plugins", "preview", project, "--target", target})
	if err != nil {
		t.Fatalf("preview: %v, %s", err, out)
	}
	var doc previewDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestPluginPreviewCompatiblePackageAndFingerprint(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/kit\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"native-kit","version":"1.0","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	writeFile(t, project, ".ai/packages/kit/skills/search/SKILL.md", "---\nname: search\ndescription: Search\n---\nSearch\n")
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"docs":{"type":"streamable-http","url":"https://example.com/mcp"}}}`)
	for _, target := range []string{"codex", "kiro"} {
		doc := getPreview(t, project, target)
		if len(doc.Plugins) != 1 || doc.Plugins[0].State != "compatible" || doc.Plugins[0].NativeIdentity != "native-kit" || len(doc.Plugins[0].Skills) != 1 || len(doc.Plugins[0].MCPServers) != 1 || doc.Fingerprint == "" {
			t.Fatalf("%s: %+v", target, doc)
		}
		if target == "kiro" && (doc.Plugins[0].Action.Kind != "command" || strings.Join(doc.Plugins[0].Action.Command, " ") != "kiro-cli powers install "+filepath.Join(project, ".ai/packages/kit")) {
			t.Fatalf("Kiro action: %+v", doc.Plugins[0].Action)
		}
		if target == "codex" && (doc.Plugins[0].Action.Kind != "manual" || len(doc.Plugins[0].Action.Instructions) == 0) {
			t.Fatalf("Codex action: %+v", doc.Plugins[0].Action)
		}
		again := getPreview(t, project, target)
		if again.Fingerprint != doc.Fingerprint {
			t.Fatal("unstable fingerprint")
		}
	}
	before := getPreview(t, project, "kiro").Fingerprint
	writeFile(t, project, ".ai/packages/kit/skills/search/SKILL.md", "---\nname: search\ndescription: Search\n---\nChanged\n")
	if getPreview(t, project, "kiro").Fingerprint == before {
		t.Fatal("package content missing from fingerprint")
	}
}
func TestPluginPreviewDoesNotExecuteOrWrite(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`)
	marker := filepath.Join(t.TempDir(), "executed")
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"x":{"type":"stdio","command":"/bin/sh","args":["-c","touch `+marker+`"]}}}`)
	before, _ := os.ReadDir(project)
	getPreview(t, project, "kiro")
	after, _ := os.ReadDir(project)
	if len(before) != len(after) {
		t.Fatal("preview wrote to project")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preview executed MCP command")
	}
}
func TestPluginPreviewBlocksNativeCollisionButNotNamespacedComponents(t *testing.T) {
	project := previewFixture(t, "plugins:\n  alpha:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/alpha\n  beta:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/beta\n")
	for _, name := range []string{"alpha", "beta"} {
		writeFile(t, project, ".ai/packages/"+name+"/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"`+name+`","version":"1","description":"Plugin","author":{"name":"Team"},"keywords":["plugin"]}`)
		writeFile(t, project, ".ai/packages/"+name+"/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"same":{"type":"streamable-http","url":"https://example.com"}}}`)
	}
	doc := getPreview(t, project, "kiro")
	if len(doc.Plugins) != 2 || len(doc.Plugins[0].Conflicts) != 0 || len(doc.Plugins[1].Conflicts) != 0 {
		t.Fatalf("namespaced MCP falsely collided: %+v", doc)
	}
	writeFile(t, project, ".ai/packages/beta/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"alpha","version":"1","description":"Plugin","author":{"name":"Team"},"keywords":["plugin"]}`)
	doc = getPreview(t, project, "kiro")
	for _, p := range doc.Plugins {
		if p.State != "compatible" || p.Action.Kind != "blocked" || len(p.Conflicts) == 0 {
			t.Fatalf("collision did not block: %+v", doc)
		}
	}
}
func TestPluginPreviewIncompatiblePendingAndUnverified(t *testing.T) {
	project := previewFixture(t, "plugins:\n  claude-only:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/claude\n  needs-token:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/token\n  remote:\n    enabled: true\n    targets:\n      codex:\n        git: https://example.com/plugin.git\n        ref: 0123456789012345678901234567890123456789\n")
	writeFile(t, project, ".ai/packages/claude/.claude-plugin/plugin.json", `{"name":"claude-only"}`)
	writeFile(t, project, ".ai/packages/token/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"token","version":"1","description":"Plugin"}`)
	writeFile(t, project, ".ai/packages/token/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"x":{"type":"stdio","command":"./bin/missing","env":{"TOKEN":"${PREVIEW_MISSING_TOKEN}"}}}}`)
	t.Setenv("PREVIEW_MISSING_TOKEN", "")
	doc := getPreview(t, project, "codex")
	states := map[string]string{}
	missing := map[string]string{}
	for _, p := range doc.Plugins {
		states[p.Name] = p.State
		missing[p.Name] = strings.Join(p.Missing, ";")
		if p.Action.Kind == "command" {
			t.Fatalf("unready plugin installable: %+v", p)
		}
	}
	// Agent Plugins mcp.json treats ${VAR} as an opaque host-resolved placeholder,
	// so only package-relative paths become requirements. Claude .mcp.json is the
	// contract that expands environment variables and reports them as missing.
	if states["claude-only"] != "incompatible" || states["needs-token"] != "pending" || states["remote"] != "unverified" || !strings.Contains(missing["needs-token"], "./bin/missing") || strings.Contains(missing["needs-token"], "environment variable:") {
		t.Fatalf("unexpected preview: %+v", doc)
	}
}
func TestPluginPreviewPartialPackageStaysPending(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1","description":"Plugin","author":{"name":"Team"},"keywords":["kit"]}`)
	writeFile(t, project, ".ai/packages/kit/hooks/hooks.json", `{}`)
	doc := getPreview(t, project, "kiro")
	if len(doc.Plugins) != 1 || doc.Plugins[0].State != "partial" || doc.Plugins[0].Action.Kind != "pending" || len(doc.Plugins[0].Omitted) == 0 {
		t.Fatalf("partial package incorrectly installable: %+v", doc)
	}
}
func TestPluginPreviewCodexMarketplaceInstructionsEscapePath(t *testing.T) {
	project := previewFixture(t, "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        path: '.ai/packages/a\"b'\n")
	writeFile(t, project, `.ai/packages/a"b/plugin.json`, `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1","description":"Plugin"}`)
	doc := getPreview(t, project, "codex")
	if len(doc.Plugins) != 1 || doc.Plugins[0].Action.Kind != "manual" {
		t.Fatalf("missing manual action: %+v", doc)
	}
	instruction := doc.Plugins[0].Action.Instructions[0]
	start := strings.Index(instruction, "{\"category\"")
	if start < 0 {
		t.Fatalf("missing JSON entry: %s", instruction)
	}
	var entry struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
	}
	if err := json.Unmarshal([]byte(instruction[start:]), &entry); err != nil || entry.Source.Path != `./.ai/packages/a"b` {
		t.Fatalf("invalid marketplace JSON: %v, %+v", err, entry)
	}
}
