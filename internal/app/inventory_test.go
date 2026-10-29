package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

type inventoryDocument struct {
	Status              string `json:"status"`
	PluginConfiguration struct {
		Marketplaces map[string]struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
		} `json:"marketplaces"`
		SharedEnvNames []string `json:"shared_env_names"`
	} `json:"plugin_configuration"`
	Resources []struct {
		Type         string   `json:"type"`
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		Sources      []string `json:"sources"`
		Destinations []string `json:"destinations"`
		Enabled      *bool    `json:"enabled"`
	} `json:"resources"`
	AdditionalFiles []string `json:"additional_files"`
	Diagnostics     []struct {
		Path    string `json:"path"`
		Unit    string `json:"unit"`
		Message string `json:"message"`
	} `json:"diagnostics"`
}

func TestInventoryReportsInvalidMCPDeclarationWithServerName(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers:\n  broken:\n    args: ['--verbose']\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid MCP declaration should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/mcp.yaml" || doc.Diagnostics[0].Unit != "broken" || !strings.Contains(doc.Diagnostics[0].Message, "command") {
		t.Fatalf("unexpected MCP diagnostic: %+v", doc)
	}
}

func TestInventoryValidatesHTTPMCPEntriesIndividually(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", `servers:
  good:
    url: https://example.com/mcp
  both:
    command: echo
    url: https://example.com/mcp
  insecure:
    url: http://example.com/mcp
`)
	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid MCP entries should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 2 {
		t.Fatalf("unexpected diagnostics: %+v", doc)
	}
	found := false
	for _, resource := range doc.Resources {
		if resource.Type == "mcp_server" && resource.Name == "good" {
			found = true
		}
	}
	if !found {
		t.Fatalf("valid HTTP server missing: %+v", doc.Resources)
	}
}

func TestInventoryKeepsValidMCPServerBesideWrongShapedServer(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers:\n  good:\n    command: echo\n  broken:\n    command: echo\n    args: wrong-shape\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid MCP declaration should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 2 || doc.Resources[1].Type != "mcp_server" || doc.Resources[1].Name != "good" {
		t.Fatalf("valid MCP server disappeared: %+v", doc)
	}
	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/mcp.yaml" || doc.Diagnostics[0].Unit != "broken" || !strings.Contains(doc.Diagnostics[0].Message, "args") {
		t.Fatalf("unexpected MCP diagnostic: %+v", doc.Diagnostics)
	}
}

func TestInventoryReportsInvalidPluginDeclarationWithIdentity(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  malformed: true\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid plugin declaration should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/plugins.yaml" || doc.Diagnostics[0].Unit != "malformed" || !strings.Contains(doc.Diagnostics[0].Message, "<plugin>@<marketplace>") {
		t.Fatalf("unexpected plugin diagnostic: %+v", doc)
	}
}

func TestInventoryKeepsValidPluginAndMarketplaceBesideWrongShapedDeclarations(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  good:\n    repo: org/good\n  broken:\n    repo: [wrong-shape]\nplugins:\n  good@good: true\n  broken@good: [wrong-shape]\nenv:\n  PUBLIC: present\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid plugin declarations should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 2 || doc.Resources[1].Name != "good@good" || doc.Resources[1].Enabled == nil || !*doc.Resources[1].Enabled {
		t.Fatalf("valid plugin disappeared: %+v", doc)
	}
	if got := doc.PluginConfiguration.Marketplaces["good"]; got.Repo != "org/good" {
		t.Fatalf("valid marketplace disappeared: %+v", doc.PluginConfiguration)
	}
	if len(doc.Diagnostics) != 2 || doc.Diagnostics[0].Path != ".ai/plugins.yaml" || doc.Diagnostics[0].Unit != "broken" || !strings.Contains(doc.Diagnostics[0].Message, "repo") || doc.Diagnostics[1].Unit != "broken@good" || !strings.Contains(doc.Diagnostics[1].Message, "bool") {
		t.Fatalf("unexpected declaration diagnostics: %+v", doc.Diagnostics)
	}
}

func TestInventoryReportsBadMCPAndPluginYAMLOncePerSource(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: [\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins: [\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("malformed canonical YAML should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 2 {
		t.Fatalf("unexpected diagnostics: %+v", doc.Diagnostics)
	}
	for i, want := range []string{".ai/mcp.yaml", ".ai/plugins.yaml"} {
		if got := doc.Diagnostics[i]; got.Path != want || got.Unit != "" || !strings.Contains(got.Message, "yaml") {
			t.Fatalf("unexpected YAML diagnostic: %+v", got)
		}
	}
}

func TestInventoryReportsInvalidMarketplaceWithoutInventingAResource(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    source: github\nplugins:\n  formatter@tools: false\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid marketplace should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/plugins.yaml" || doc.Diagnostics[0].Unit != "tools" || !strings.Contains(doc.Diagnostics[0].Message, "repo") {
		t.Fatalf("unexpected marketplace diagnostic: %+v", doc)
	}
	if len(doc.Resources) != 2 || doc.Resources[1].Type != "plugin" || doc.Resources[1].Name != "formatter@tools" {
		t.Fatalf("marketplace should not be a separate resource: %+v", doc.Resources)
	}
}

func TestInventoryListsNamedMCPServersWithoutRunningThem(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers:\n  zeta:\n    command: sh\n    args: ['-c', 'touch "+filepath.Join(project, "mcp-ran")+"']\n  alpha:\n    command: npx\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Resources) != 3 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	for i, name := range []string{"alpha", "zeta"} {
		got := doc.Resources[i+1]
		if got.Type != "mcp_server" || got.Name != name || !reflect.DeepEqual(got.Sources, []string{".ai/mcp.yaml"}) || !reflect.DeepEqual(got.Destinations, []string{".codex/config.toml", ".kiro/settings/mcp.json", ".mcp.json"}) {
			t.Fatalf("unexpected MCP resource: %+v", got)
		}
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{}) {
		t.Fatalf("recognized MCP source remains additional: %v", doc.AdditionalFiles)
	}
	if _, err := os.Stat(filepath.Join(project, "mcp-ran")); !os.IsNotExist(err) {
		t.Fatalf("inventory ran MCP command: %v", err)
	}
}

func TestInventoryListsDeclaredPluginsIncludingDisabledWithoutInstalling(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  zeta@tools: false\n  alpha@tools: true\nenv:\n  SHARED_TOKEN: example\n")
	bin := t.TempDir()
	marker := filepath.Join(project, "install-ran")
	writeFile(t, bin, "claude", "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Resources) != 3 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	for i, want := range []struct {
		name    string
		enabled bool
	}{{"alpha@tools", true}, {"zeta@tools", false}} {
		got := doc.Resources[i+1]
		if got.Type != "plugin" || got.Name != want.name || got.Enabled == nil || *got.Enabled != want.enabled || !reflect.DeepEqual(got.Sources, []string{".ai/plugins.yaml"}) || !reflect.DeepEqual(got.Destinations, []string{".claude/settings.json"}) {
			t.Fatalf("unexpected plugin resource: %+v", got)
		}
	}
	if len(doc.AdditionalFiles) != 0 {
		t.Fatalf("plugin marketplace or env inventoried as additional: %v", doc.AdditionalFiles)
	}
	if got := doc.PluginConfiguration.Marketplaces["tools"]; got.Source != "github" || got.Repo != "org/tools" {
		t.Fatalf("marketplace declaration was lost: %+v", doc.PluginConfiguration)
	}
	if !reflect.DeepEqual(doc.PluginConfiguration.SharedEnvNames, []string{"SHARED_TOKEN"}) || strings.Contains(out, "example") {
		t.Fatalf("shared env names should be visible without values: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inventory ran plugin installer: %v", err)
	}
}

func TestInventoryInspectsPortablePluginAsOneUnit(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  toolkit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/toolkit\n      kiro:\n        path: .ai/packages/toolkit\n")
	writeFile(t, project, ".ai/packages/toolkit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"toolkit","version":"1.0.0","description":"Tools","author":{"name":"Team"},"keywords":["toolkit"]}`)
	writeFile(t, project, ".ai/packages/toolkit/skills/search/SKILL.md", "---\nname: search\ndescription: Search things\n---\n\nSearch.\n")
	writeFile(t, project, ".ai/packages/toolkit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"docs":{"type":"streamable-http","url":"https://example.com/mcp"}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil {
		t.Fatal(err)
	}
	again, err := app.Run(project, []string{"inventory", project})
	if err != nil || again != out {
		t.Fatalf("portable inventory is not deterministic: %v", err)
	}
	var doc struct {
		Resources []struct {
			Type     string `json:"type"`
			Name     string `json:"name"`
			Variants []struct {
				Target     string   `json:"target"`
				Status     string   `json:"status"`
				Skills     []string `json:"skills"`
				MCPServers []string `json:"mcp_servers"`
			} `json:"variants"`
		} `json:"resources"`
		AdditionalFiles []string `json:"additional_files"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Resources) != 2 || doc.Resources[1].Name != "toolkit" || len(doc.Resources[1].Variants) != 2 {
		t.Fatalf("unexpected plugin inventory: %s", out)
	}
	for _, variant := range doc.Resources[1].Variants {
		if variant.Status != "compatible" || !reflect.DeepEqual(variant.Skills, []string{"search"}) || !reflect.DeepEqual(variant.MCPServers, []string{"docs"}) {
			t.Fatalf("unexpected variant: %+v", variant)
		}
	}
	if len(doc.AdditionalFiles) != 0 {
		t.Fatalf("package files should belong to plugin: %v", doc.AdditionalFiles)
	}
}

func TestInventoryReportsClaudeOnlyAndUnverifiedPluginVariants(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  claude-kit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/claude-kit\n      kiro:\n        git: https://example.com/kit.git\n        ref: 0123456789012345678901234567890123456789\n")
	writeFile(t, project, ".ai/packages/claude-kit/.claude-plugin/plugin.json", `{"name":"claude-kit"}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Resources []struct {
			Variants []struct {
				Target      string   `json:"target"`
				Status      string   `json:"status"`
				Skills      []string `json:"skills"`
				Unsupported []string `json:"unsupported"`
			} `json:"variants"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	variants := doc.Resources[1].Variants
	if len(variants) != 2 || variants[0].Status != "incompatible" || variants[1].Status != "unverified" || len(variants[1].Skills) != 0 || len(variants[0].Unsupported) == 0 {
		t.Fatalf("wrong compatibility: %s", out)
	}
}

func TestInventoryRejectsPackageSymlinkEscape(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  escape:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/escape\n")
	if err := os.MkdirAll(filepath.Join(project, ".ai/packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(project, ".ai/packages/escape")); err != nil {
		t.Fatal(err)
	}
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, "escapes .ai/") {
		t.Fatalf("symlink escape accepted: %s %v", out, err)
	}
}

func TestInventoryRejectsNestedPackageSymlinkBeforeReadingIt(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  linked:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/linked\n")
	writeFile(t, project, ".ai/packages/linked/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"linked"}`)
	if err := os.Symlink(t.TempDir(), filepath.Join(project, ".ai/packages/linked/skills")); err != nil {
		t.Fatal(err)
	}
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, "symlink in package is unsupported") {
		t.Fatalf("nested symlink accepted: %s %v", out, err)
	}
}

func TestInventoryReportsMissingPluginRequirementsWithoutRunningCommands(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  local:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/local\n")
	writeFile(t, project, ".ai/packages/local/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"local","version":"1.0.0","description":"Local"}`)
	writeFile(t, project, ".ai/packages/local/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"tool":{"type":"stdio","command":"./bin/missing","env":{"TOKEN":"${MISSING_PLUGIN_TOKEN}"}}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"status":"pending"`) || strings.Contains(out, "environment variable: MISSING_PLUGIN_TOKEN") || !strings.Contains(out, "MCP command path: ./bin/missing") {
		t.Fatalf("missing requirements not reported: %s", out)
	}
}

func TestInventoryFlagsNativePluginIdentityCollision(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  alpha:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/alpha\n  beta:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/beta\n")
	for _, name := range []string{"alpha", "beta"} {
		writeFile(t, project, ".ai/packages/"+name+"/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"same","version":"1.0.0","description":"Same"}`)
	}
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, `"unit":"beta"`) || !strings.Contains(out, `package \"same\" also belongs to plugin \"alpha\"`) {
		t.Fatalf("collision not reported: %s %v", out, err)
	}
}

func TestInventoryKeepsValidStructuredPluginBesideUnsafePath(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  unsafe:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/../outside\n  remote:\n    enabled: false\n    targets:\n      codex:\n        git: https://example.com/remote.git\n        ref: 0123456789012345678901234567890123456789\n")
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil {
		t.Fatal("unsafe path accepted")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 2 || doc.Resources[1].Name != "remote" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Unit != "unsafe" {
		t.Fatalf("valid neighbor lost: %s", out)
	}
}

func TestKiroVariantWithoutActivationMetadataIsIncompatible(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  plain:\n    enabled: true\n    targets:\n      kiro:\n        path: .ai/packages/plain\n")
	writeFile(t, project, ".ai/packages/plain/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"plain","version":"1.0.0","description":"Plain"}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"status":"incompatible"`) || !strings.Contains(out, "Kiro manifest author.name") || !strings.Contains(out, "Kiro manifest keywords") {
		t.Fatalf("Kiro metadata not checked: %s %v", out, err)
	}
}

func TestMissingLocalPluginCanStillBeInventoried(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  absent:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/absent\n")
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"status":"unverified"`) || !strings.Contains(out, "package path: .ai/packages/absent") {
		t.Fatalf("missing package should be copyable: %s %v", out, err)
	}
}

func TestCodexAcceptsMinimalAgentPluginsManifest(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  small:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/small\n")
	writeFile(t, project, ".ai/packages/small/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"small"}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"status":"compatible"`) {
		t.Fatalf("valid manifest rejected: %s %v", out, err)
	}
}

func portableInventoryFixture(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit"}`)
	return project
}

func TestPortableMCPRejectsBadRemoteAndKeepsGoodServer(t *testing.T) {
	for _, tc := range []struct{ name, url, headers string }{
		{"relative", `/mcp`, `{}`},
		{"insecure", `http://example.com/mcp`, `{}`},
		{"userinfo", `https://user@example.com/mcp`, `{}`},
		{"fragment", `https://example.com/mcp#part`, `{}`},
		{"duplicate headers", `https://example.com/mcp`, `{"X-Test":"one","x-test":"two"}`},
		{"bad header name", `https://example.com/mcp`, `{"Bad Header":"one"}`},
		{"bad header value", `https://example.com/mcp`, `{"X-Test":"one\r\ntwo"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := portableInventoryFixture(t)
			writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"bad":{"type":"streamable-http","url":"`+tc.url+`","headers":`+tc.headers+`},"good":{"type":"sse","url":"http://127.0.0.1/mcp","headers":{"X-Literal":"${TOKEN}"}}}}`)
			out, err := app.Run(project, []string{"inventory", project})
			if err == nil || !strings.Contains(out, `MCP server bad:`) || !strings.Contains(out, `"mcp_servers":["good"]`) || strings.Contains(out, "environment variable: TOKEN") {
				t.Fatalf("bad remote or valid neighbor handled incorrectly: %s %v", out, err)
			}
		})
	}
}

func TestPortableMCPCommandIsOneExecutableAndUnknownPlaceholderIsLiteral(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"shell":{"type":"stdio","command":"node script.js"},"placeholder":{"type":"stdio","command":"${TOOL}"},"good":{"type":"stdio","command":"node","args":["${TOKEN}","${PLUGIN_ROOT}/script.js"],"env":{"TOKEN":"${TOKEN}"}}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, `"mcp_servers":["good"]`) || strings.Contains(out, "environment variable: TOKEN") || !strings.Contains(out, "MCP server shell:") || !strings.Contains(out, "MCP server placeholder:") {
		t.Fatalf("command validation or placeholder semantics wrong: %s %v", out, err)
	}
}

func TestPortableMCPWrongShapedServerKeepsSibling(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"broken":"not an object","good":{"type":"stdio","command":"node"}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, `"mcp_servers":["good"]`) || !strings.Contains(out, "MCP server broken:") {
		t.Fatalf("bad server hid valid sibling: %s %v", out, err)
	}
}

func TestPortableSkillNameMatchesDirectoryAndKeepsSibling(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/packages/kit/skills/good/SKILL.md", "---\nname: good\ndescription: Good skill\n---\n\nText\n")
	writeFile(t, project, ".ai/packages/kit/skills/bad/SKILL.md", "---\nname: other\ndescription: Bad skill\n---\n\nText\n")
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, `"skills":["good"]`) || !strings.Contains(out, "invalid or missing skill frontmatter") {
		t.Fatalf("skill validation lost valid sibling: %s %v", out, err)
	}
}

func TestPortableSkillRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"Bad", "bad--name", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			project := portableInventoryFixture(t)
			writeFile(t, project, ".ai/packages/kit/skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: A skill\n---\n")
			out, err := app.Run(project, []string{"inventory", project})
			if err == nil || !strings.Contains(out, `"skills":[]`) {
				t.Fatalf("invalid skill name accepted: %s %v", out, err)
			}
		})
	}
}

func TestPortableCatalogShowsComponentSources(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/packages/kit/skills/search/SKILL.md", "---\nname: search\ndescription: Search things\n---\n")
	writeFile(t, project, ".ai/packages/kit/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"docs":{"type":"streamable-http","url":"https://example.com/mcp"}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"skill_sources":{"search":".ai/packages/kit/skills/search/SKILL.md"}`) || !strings.Contains(out, `"mcp_server_sources":{"docs":".ai/packages/kit/mcp.json"}`) {
		t.Fatalf("component provenance missing: %s %v", out, err)
	}
}

func TestDisabledPluginDoesNotCollideWithEnabledPackage(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/kit\n  spare:\n    enabled: false\n    targets:\n      codex:\n        path: .ai/packages/spare\n")
	writeFile(t, project, ".ai/packages/spare/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit"}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || strings.Contains(out, "also belongs to plugin") {
		t.Fatalf("disabled package created active collision: %s %v", out, err)
	}
}

func TestCodexOverlayIsNotClaimedCompatibleWithoutValidation(t *testing.T) {
	project := portableInventoryFixture(t)
	writeFile(t, project, ".ai/packages/kit/.codex-plugin/plugin.json", `{"apps":"./.app.json"}`)
	writeFile(t, project, ".ai/packages/kit/.app.json", `{"apps":{}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"status":"pending"`) || !strings.Contains(out, ".codex-plugin/plugin.json") || !strings.Contains(out, ".app.json") {
		t.Fatalf("unvalidated Codex overlay claimed compatible: %s %v", out, err)
	}
}

func TestInventoryChecksMCPArgumentsAndWorkingDirectory(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  tool:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/tool\n")
	writeFile(t, project, ".ai/packages/tool/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"tool"}`)
	writeFile(t, project, ".ai/packages/tool/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"local":{"type":"stdio","command":"node","args":["./missing.js","${MISSING_ARG_TOKEN}"],"cwd":"./missing-dir"}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, "MCP argument path: ./missing.js") || !strings.Contains(out, "MCP working directory: ./missing-dir") || strings.Contains(out, "environment variable: MISSING_ARG_TOKEN") {
		t.Fatalf("MCP requirements missing: %s %v", out, err)
	}
}

func TestInventoryRejectsUnknownPortableMCPFields(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  tool:\n    enabled: true\n    targets:\n      codex:\n        path: .ai/packages/tool\n")
	writeFile(t, project, ".ai/packages/tool/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"tool"}`)
	writeFile(t, project, ".ai/packages/tool/mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"local":{"type":"stdio","command":"node","unknown":true}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err == nil || !strings.Contains(out, `unknown JSON field \"unknown\"`) {
		t.Fatalf("unknown field accepted: %s %v", out, err)
	}
}

func TestInventoryAttributesClaudeMCPAndReportsArgumentRequirements(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  kit:\n    enabled: true\n    targets:\n      claude:\n        install: kit@tools\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/.claude-plugin/plugin.json", `{"name":"kit"}`)
	writeFile(t, project, ".ai/packages/kit/.mcp.json", `{"mcpServers":{"local":{"command":"./bin/missing","args":["${MISSING_ARG_TOKEN}"]}}}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"mcp_servers":["local"]`) || !strings.Contains(out, "environment variable: MISSING_ARG_TOKEN") || !strings.Contains(out, "MCP command path: ./bin/missing") {
		t.Fatalf("Claude contributions missing: %s %v", out, err)
	}
}

func TestInventoryDoesNotAssumePortableRootWorksInClaude(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  kit:\n    enabled: true\n    targets:\n      claude:\n        install: kit@tools\n        path: .ai/packages/kit\n")
	writeFile(t, project, ".ai/packages/kit/plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit"}`)
	out, err := app.Run(project, []string{"inventory", project})
	if err != nil || !strings.Contains(out, `"status":"incompatible"`) || !strings.Contains(out, "Claude-specific manifest unavailable") {
		t.Fatalf("portable package falsely claimed Claude compatibility: %s %v", out, err)
	}
}

func TestStructuredClaudePluginKeepsLegacyRenderAndInstall(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  toolkit:\n    enabled: true\n    targets:\n      claude:\n        install: toolkit@tools\n      codex:\n        git: https://example.com/toolkit.git\n        ref: 0123456789012345678901234567890123456789\n")
	out, err := app.Run(project, []string{"list", "--target", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ".claude/settings.json") {
		t.Fatal(out)
	}
	settings, err := app.Run(project, []string{"--target", "claude", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(settings, ".claude/settings.json") {
		t.Fatal(settings)
	}
	install, err := app.Run(project, []string{"plugins", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(install, "claude plugin install toolkit@tools") {
		t.Fatal(install)
	}
}

func decodeInventory(t *testing.T, out string) inventoryDocument {
	t.Helper()
	var doc inventoryDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("inventory must be one JSON document: %v\n%s", err, out)
	}
	return doc
}

func TestInventoryRequiresExplicitProjectAndDocumentsUsage(t *testing.T) {
	out, err := app.Run(t.TempDir(), []string{"inventory"})
	if err == nil {
		t.Fatal("inventory without project should fail")
	}
	if !strings.Contains(err.Error(), "project") || out != "" {
		t.Fatalf("unexpected missing argument result: %q, %v", out, err)
	}
	help, err := app.Run(t.TempDir(), []string{"--help"})
	if err != nil || !strings.Contains(help, "ai-sync inventory <project-dir>") {
		t.Fatalf("help must document explicit project directory: %q, %v", help, err)
	}
}

func TestInventoryReadsExplicitProjectWithoutWritingOrRunningSetup(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/targets/kiro.md", "# Kiro\n")
	writeFile(t, project, ".ai/targets/claude.md", "# Claude\n")
	writeFile(t, project, ".ai/targets/codex.md", "# Codex\n")
	writeFile(t, project, ".ai/notes.txt", "keep me\n")
	marker := filepath.Join(project, "setup-ran")
	writeFile(t, project, ".ai/setup.yaml", "probe:\n  check: 'touch "+marker+"'\n  install: 'touch "+marker+"'\n")

	out, err := app.Run(other, []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Diagnostics) != 0 {
		t.Fatalf("unexpected inventory status: %+v", doc)
	}
	if len(doc.Resources) != 5 {
		t.Fatalf("expected shared and three target instructions plus setup entry, got %+v", doc.Resources)
	}
	if got := doc.Resources[0]; got.Type != "shared_instruction" || got.Name != "project" || !reflect.DeepEqual(got.Sources, []string{".ai/project.md"}) || !reflect.DeepEqual(got.Destinations, []string{".kiro/steering/project-conventions.md", "AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("unexpected shared instruction: %+v", got)
	}
	for i, want := range []struct{ name, source, destination string }{
		{"claude", ".ai/targets/claude.md", "CLAUDE.md"},
		{"codex", ".ai/targets/codex.md", "AGENTS.md"},
		{"kiro", ".ai/targets/kiro.md", ".kiro/steering/project-conventions.md"},
	} {
		got := doc.Resources[i+1]
		if got.Type != "agent_instruction" || got.Name != want.name || !reflect.DeepEqual(got.Sources, []string{want.source}) || !reflect.DeepEqual(got.Destinations, []string{want.destination}) {
			t.Fatalf("unexpected target instruction: %+v", got)
		}
	}
	if got := doc.Resources[4]; got.Type != "setup_entry" || got.Name != "probe" || !reflect.DeepEqual(got.Sources, []string{".ai/setup.yaml"}) || len(got.Destinations) != 0 {
		t.Fatalf("unexpected setup entry: %+v", got)
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/notes.txt"}) {
		t.Fatalf("unexpected additional files: %v", doc.AdditionalFiles)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inventory ran setup command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("inventory wrote generated output: %v", err)
	}
	again, err := app.Run(other, []string{"inventory", project})
	if err != nil || again != out {
		t.Fatalf("inventory output changed: %v\n%s\n%s", err, out, again)
	}
}

func TestInventoryIncludesPathRulesAndCanonicalAgents(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/rules/frontend.md", "---\npaths:\n  - frontend/**\n  - '*.go'\n---\n\n# Frontend\n")
	writeFile(t, project, ".ai/rules/backend.md", "---\npaths:\n  - backend/**\n---\n\n# Backend\n")
	writeFile(t, project, ".ai/agents/reviewer.md", "---\nname: reviewer\ndescription: Review code\n---\n")
	writeFile(t, project, ".ai/agents/author.md", "---\nname: author\ndescription: Write code\n---\n")
	before := snapshotProject(t, project)

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Diagnostics) != 0 || len(doc.Resources) != 5 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	for i, want := range []struct {
		kind, name, source string
		destinations       []string
	}{
		{"path_rule", "backend", ".ai/rules/backend.md", []string{".claude/rules/backend.md", ".kiro/steering/backend.md", "backend/AGENTS.md"}},
		{"path_rule", "frontend", ".ai/rules/frontend.md", []string{".claude/rules/frontend.md", ".kiro/steering/frontend.md", "AGENTS.md", "frontend/AGENTS.md"}},
		{"agent", "author", ".ai/agents/author.md", []string{".claude/agents/author.md"}},
		{"agent", "reviewer", ".ai/agents/reviewer.md", []string{".claude/agents/reviewer.md"}},
	} {
		got := doc.Resources[i+1]
		if got.Type != want.kind || got.Name != want.name || !reflect.DeepEqual(got.Sources, []string{want.source}) || !reflect.DeepEqual(got.Destinations, want.destinations) {
			t.Fatalf("resource %d: got %+v, want %+v", i+1, got, want)
		}
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{}) {
		t.Fatalf("classified files leaked into additional_files: %v", doc.AdditionalFiles)
	}
	if after := snapshotProject(t, project); !reflect.DeepEqual(after, before) {
		t.Fatalf("inventory changed project tree:\nbefore: %v\nafter: %v", before, after)
	}
}

func TestInventoryReportsMalformedRulesAndKeepsReadableResources(t *testing.T) {
	for _, testCase := range []struct{ name, contents, cause string }{
		{"missing paths", "---\ndescription: No paths\n---\n\n# Broken\n", "paths must contain at least one glob"},
		{"invalid YAML", "---\npaths: [\n---\n\n# Broken\n", "yaml"},
		{"missing opening delimiter", "paths:\n  - src/**\n---\n", "missing YAML frontmatter"},
		{"malformed closing delimiter", "---\npaths:\n  - src/**\n---oops\n\n# Broken\n", "missing closing frontmatter delimiter"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			project := t.TempDir()
			writeFile(t, project, ".ai/project.md", "# Shared\n")
			writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
			writeFile(t, project, ".ai/rules/broken.md", testCase.contents)
			writeFile(t, project, ".ai/rules/good.md", "---\npaths:\n  - src/**\n---\n\n# Good\n")
			writeFile(t, project, ".ai/agents/reviewer.md", "---\nname: reviewer\ndescription: Review code\n---\n")
			before := snapshotProject(t, project)

			out, err := app.Run(t.TempDir(), []string{"inventory", project})
			if err == nil {
				t.Fatal("malformed rule should return an error")
			}
			doc := decodeInventory(t, out)
			if doc.Status != "invalid" || len(doc.Resources) != 3 || len(doc.Diagnostics) != 1 {
				t.Fatalf("unexpected invalid inventory: %+v", doc)
			}
			if got := doc.Diagnostics[0]; got.Path != ".ai/rules/broken.md" || !strings.Contains(got.Message, testCase.cause) {
				t.Fatalf("diagnostic lacks source and cause: %+v", got)
			}
			if doc.Resources[1].Name != "good" || doc.Resources[2].Name != "reviewer" {
				t.Fatalf("readable neighbors disappeared: %+v", doc.Resources)
			}
			if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/rules/broken.md"}) {
				t.Fatalf("unexpected additional files: %v", doc.AdditionalFiles)
			}
			if after := snapshotProject(t, project); !reflect.DeepEqual(after, before) {
				t.Fatalf("inventory changed project tree:\nbefore: %v\nafter: %v", before, after)
			}
		})
	}
}

func TestInventoryReportsMissingAndInvalidSourcesAsJSON(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	out, err := app.Run(other, []string{"inventory", project})
	if err == nil {
		t.Fatal("missing source should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "missing" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai" || !strings.Contains(doc.Diagnostics[0].Message, "not found") {
		t.Fatalf("unexpected missing inventory: %+v", doc)
	}
	writeFile(t, project, ".ai/targets/claude.md", "# Claude\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	out, err = app.Run(other, []string{"inventory", project})
	if err == nil {
		t.Fatal("source without project.md should fail")
	}
	doc = decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/project.md" || !strings.Contains(doc.Diagnostics[0].Message, "not found") || len(doc.Resources) != 1 {
		t.Fatalf("unexpected invalid inventory: %+v", doc)
	}
}

func TestInventoryReportsMalformedYAMLWithoutHidingInstructions(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/targets/codex.md", "# Codex\n")
	writeFile(t, project, ".ai/setup.yaml", "broken: [\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("malformed YAML should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 2 || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/setup.yaml" || !strings.Contains(doc.Diagnostics[0].Message, "yaml") {
		t.Fatalf("unexpected malformed YAML inventory: %+v", doc)
	}
}

func TestInventoryRequiresCanonicalMCPButDoesNotParseAuxiliaryYAML(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/notes.yaml", "not valid: [\n")
	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("missing canonical mcp.yaml should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/mcp.yaml" {
		t.Fatalf("unexpected missing MCP inventory: %+v", doc)
	}
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	out, err = app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil || decodeInventory(t, out).Status != "ok" {
		t.Fatalf("auxiliary YAML should remain an additional file: %v\n%s", err, out)
	}
}

func TestInventoryGroupsSkillAndAuxiliaryFiles(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/skills/review/SKILL.md", "---\nname: review\ndescription: Review code.\n---\n\n# Review\n")
	writeFile(t, project, ".ai/skills/review/references/checklist.md", "# Checklist\n")
	writeFile(t, project, ".ai/notes.txt", "keep\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Resources) != 2 || len(doc.Diagnostics) != 0 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	skill := doc.Resources[1]
	if skill.Type != "skill" || skill.Name != "review" || skill.Status != "ok" || !reflect.DeepEqual(skill.Sources, []string{
		".ai/skills/review/SKILL.md", ".ai/skills/review/references/checklist.md",
	}) || !reflect.DeepEqual(skill.Destinations, []string{
		".agents/skills/review/SKILL.md", ".agents/skills/review/references/checklist.md",
		".claude/skills/review/SKILL.md", ".claude/skills/review/references/checklist.md",
		".kiro/powers/review/POWER.md", ".kiro/powers/review/references/checklist.md",
		".kiro/skills/review/SKILL.md", ".kiro/skills/review/references/checklist.md",
	}) {
		t.Fatalf("unexpected skill resource: %+v", skill)
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/notes.txt"}) {
		t.Fatalf("skill files should not be additional: %v", doc.AdditionalFiles)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("inventory wrote generated skill files: %v", err)
	}
}

func TestInventoryReportsSkillWithoutSkillFileAndKeepsOtherResources(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/targets/codex.md", "# Codex\n")
	writeFile(t, project, ".ai/skills/broken/references/note.md", "# Note\n")
	writeFile(t, project, ".ai/skills/good/SKILL.md", "---\nname: good\ndescription: Good.\n---\n\n# Good\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("skill without SKILL.md should invalidate inventory")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 4 || doc.Resources[2].Type != "skill" || doc.Resources[2].Name != "broken" || doc.Resources[3].Name != "good" {
		t.Fatalf("inventory should retain both skills and instructions: %+v", doc)
	}
	if doc.Resources[2].Status != "invalid" || doc.Resources[3].Status != "ok" {
		t.Fatalf("skill validity is not explicit on each resource: %+v", doc.Resources)
	}
	if len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/skills/broken/SKILL.md" || !strings.Contains(doc.Diagnostics[0].Message, "not found") {
		t.Fatalf("missing skill file needs a path and cause: %+v", doc.Diagnostics)
	}
	if !reflect.DeepEqual(doc.Resources[2].Destinations, []string{
		".agents/skills/broken/references/note.md", ".claude/skills/broken/references/note.md",
		".kiro/powers/broken/POWER.md", ".kiro/powers/broken/references/note.md",
		".kiro/skills/broken/references/note.md",
	}) {
		t.Fatalf("invalid skill should include current Kiro Power destination: %+v", doc.Resources[2])
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{}) {
		t.Fatalf("skill auxiliary should belong to invalid skill: %v", doc.AdditionalFiles)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("inventory wrote generated skill files: %v", err)
	}
}

func TestInventoryReportsMalformedAndUnreadableSkillFiles(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/skills/malformed/SKILL.md", "---\nname: [\n---\n\n# Broken\n")
	writeFile(t, project, ".ai/skills/unreadable/SKILL.md", "---\nname: unreadable\ndescription: Unreadable.\n---\n")
	if err := os.Remove(filepath.Join(project, ".ai/skills/unreadable/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.md", filepath.Join(project, ".ai/skills/unreadable/SKILL.md")); err != nil {
		t.Fatal(err)
	}

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid skill files should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 3 || len(doc.Diagnostics) != 2 {
		t.Fatalf("unexpected invalid skill inventory: %+v", doc)
	}
	if doc.Resources[1].Status != "invalid" || doc.Resources[2].Status != "invalid" {
		t.Fatalf("malformed and unreadable skills must be marked invalid: %+v", doc.Resources)
	}
	if doc.Diagnostics[0].Path != ".ai/skills/malformed/SKILL.md" || !strings.Contains(doc.Diagnostics[0].Message, "yaml") {
		t.Fatalf("malformed skill needs YAML cause: %+v", doc.Diagnostics)
	}
	if doc.Diagnostics[1].Path != ".ai/skills/unreadable/SKILL.md" || !strings.Contains(doc.Diagnostics[1].Message, "not found") {
		t.Fatalf("unreadable skill needs path and cause: %+v", doc.Diagnostics)
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{}) {
		t.Fatalf("invalid skill files should remain owned by skills: %v", doc.AdditionalFiles)
	}
}

func TestInventoryListsHookEventsAndNamedSetupEntriesWithoutExecutingCommands(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	markers := map[string]string{}
	for _, name := range []string{"mcp", "session_hook", "edit_hook", "alpha_check", "alpha_install", "zeta_check", "zeta_install"} {
		markers[name] = filepath.Join(project, name+"-ran")
	}
	writeFile(t, project, ".ai/mcp.yaml", "servers:\n  marker:\n    command: touch\n    args: ['"+markers["mcp"]+"']\n")
	writeFile(t, project, ".ai/hooks.yaml", "SessionStart:\n  - hooks:\n      - type: command\n        command: 'touch "+markers["session_hook"]+"'\nPostEdit:\n  - matcher: Write\n    hooks:\n      - type: command\n        command: 'touch "+markers["edit_hook"]+"'\n")
	writeFile(t, project, ".ai/setup.yaml", "zeta:\n  check: 'touch "+markers["zeta_check"]+"'\n  install: 'touch "+markers["zeta_install"]+"'\nalpha:\n  check: 'touch "+markers["alpha_check"]+"'\n  install: 'touch "+markers["alpha_install"]+"'\n")
	before := snapshotProject(t, project)

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err != nil {
		t.Fatalf("inventory failed: %v", err)
	}
	doc := decodeInventory(t, out)
	if doc.Status != "ok" || len(doc.Diagnostics) != 0 || len(doc.Resources) != 6 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	if got := doc.Resources[1]; got.Type != "mcp_server" || got.Name != "marker" {
		t.Fatalf("unexpected MCP resource: %+v", got)
	}
	for i, want := range []struct {
		kind, name, source string
		destinations       []string
	}{
		{"hook_event", "PostEdit", ".ai/hooks.yaml", []string{".claude/settings.json"}},
		{"hook_event", "SessionStart", ".ai/hooks.yaml", []string{".claude/settings.json"}},
		{"setup_entry", "alpha", ".ai/setup.yaml", []string{}},
		{"setup_entry", "zeta", ".ai/setup.yaml", []string{}},
	} {
		got := doc.Resources[i+2]
		if got.Type != want.kind || got.Name != want.name || !reflect.DeepEqual(got.Sources, []string{want.source}) || !reflect.DeepEqual(got.Destinations, want.destinations) {
			t.Fatalf("resource %d: %+v", i+2, got)
		}
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{}) {
		t.Fatalf("unexpected additional files: %v", doc.AdditionalFiles)
	}
	for name, marker := range markers {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("inventory executed %s command: %v", name, err)
		}
	}
	if after := snapshotProject(t, project); !reflect.DeepEqual(after, before) {
		t.Fatalf("inventory changed project tree:\nbefore: %v\nafter: %v", before, after)
	}
}

func snapshotProject(t *testing.T, project string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(project, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(project, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[rel] = "directory"
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(contents)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot project: %v", err)
	}
	return files
}

func TestInventoryReportsMalformedHooksYAMLAndKeepsSetupEntries(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/hooks.yaml", "SessionStart: [\n")
	writeFile(t, project, ".ai/setup.yaml", "tool:\n  check: echo ready\n  install: echo install\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("malformed hooks YAML should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 2 || doc.Resources[1].Type != "setup_entry" || doc.Resources[1].Name != "tool" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/hooks.yaml" || !strings.Contains(doc.Diagnostics[0].Message, "yaml") {
		t.Fatalf("unexpected malformed hooks inventory: %+v", doc)
	}
}

func TestInventoryRejectsDuplicateSetupNames(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/setup.yaml", "tool:\n  check: echo one\n  install: echo one\ntool:\n  check: echo two\n  install: echo two\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("duplicate setup names should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Path != ".ai/setup.yaml" || !strings.Contains(doc.Diagnostics[0].Message, "tool") || !strings.Contains(doc.Diagnostics[0].Message, "duplicate") {
		t.Fatalf("unexpected duplicate setup diagnostic: %+v", doc)
	}
}

func TestInventoryReportsInvalidNamedEntriesAndKeepsValidNeighbors(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/hooks.yaml", "Broken:\n  - matcher: Write\nGood:\n  - hooks:\n      - type: command\n        command: echo safe\n")
	writeFile(t, project, ".ai/setup.yaml", "broken:\n  check: true\ngood:\n  check: echo safe\n  install: echo safe\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid declarations should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 3 || len(doc.Diagnostics) != 2 {
		t.Fatalf("unexpected inventory: %+v", doc)
	}
	if doc.Resources[1].Type != "hook_event" || doc.Resources[1].Name != "Good" || doc.Resources[2].Type != "setup_entry" || doc.Resources[2].Name != "good" {
		t.Fatalf("valid neighboring entries were hidden: %+v", doc.Resources)
	}
	for _, want := range []struct{ path, name, cause string }{
		{".ai/hooks.yaml", "Broken", "needs at least one hook"},
		{".ai/setup.yaml", "broken", "needs a check and an install"},
	} {
		found := false
		for _, diagnostic := range doc.Diagnostics {
			if diagnostic.Path == want.path && diagnostic.Unit == want.name && strings.Contains(diagnostic.Message, want.cause) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing actionable diagnostic for %s: %+v", want.name, doc.Diagnostics)
		}
	}
}

func TestInventoryKeepsValidHooksAndSetupBesideWrongShapedEntries(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers: {}\n")
	writeFile(t, project, ".ai/hooks.yaml", "Bad: wrong-shape\nGood:\n  - hooks:\n      - type: command\n        command: echo safe\n")
	writeFile(t, project, ".ai/setup.yaml", "bad:\n  check: [wrong-shape]\n  install: echo safe\ngood:\n  check: echo safe\n  install: echo safe\n")

	out, err := app.Run(t.TempDir(), []string{"inventory", project})
	if err == nil {
		t.Fatal("invalid declarations should fail")
	}
	doc := decodeInventory(t, out)
	if doc.Status != "invalid" || len(doc.Resources) != 3 || doc.Resources[1].Name != "Good" || doc.Resources[2].Name != "good" {
		t.Fatalf("valid named declarations disappeared: %+v", doc)
	}
	if len(doc.Diagnostics) != 2 || doc.Diagnostics[0].Path != ".ai/hooks.yaml" || doc.Diagnostics[0].Unit != "Bad" || doc.Diagnostics[1].Path != ".ai/setup.yaml" || doc.Diagnostics[1].Unit != "bad" {
		t.Fatalf("invalid named declaration lacks source and unit: %+v", doc.Diagnostics)
	}
}

func TestInventoryCompleteProjectIsStableAndReadOnly(t *testing.T) {
	project := t.TempDir()
	caller := t.TempDir()
	marker := filepath.Join(project, "command-ran")
	writeFile(t, project, ".ai/project.md", "# Shared\n")
	writeFile(t, project, ".ai/targets/codex.md", "# Codex\n")
	writeFile(t, project, ".ai/rules/zeta.md", "---\npaths: [src/**]\n---\n# Zeta\n")
	writeFile(t, project, ".ai/rules/alpha.md", "---\npaths: [docs/**]\n---\n# Alpha\n")
	writeFile(t, project, ".ai/agents/reviewer.md", "---\nname: reviewer\ndescription: Review code\n---\n# Reviewer\n")
	writeFile(t, project, ".ai/skills/review/SKILL.md", "---\nname: review\ndescription: Review code\n---\n# Review\n")
	writeFile(t, project, ".ai/skills/review/references/checklist.md", "# Checklist\n")
	writeFile(t, project, ".ai/mcp.yaml", "servers:\n  zeta:\n    command: touch\n    args: ['"+marker+"']\n  alpha:\n    command: echo\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\nplugins:\n  zeta@tools: false\n  alpha@tools: true\n")
	writeFile(t, project, ".ai/hooks.yaml", "SessionStart:\n  - hooks:\n      - type: command\n        command: touch "+marker+"\n")
	writeFile(t, project, ".ai/setup.yaml", "tool:\n  check: touch "+marker+"\n  install: touch "+marker+"\n")
	writeFile(t, project, ".ai/z-last.txt", "extra\n")
	writeFile(t, project, ".ai/assets/a-first.txt", "extra\n")
	before := snapshotProject(t, project)

	legacyList, err := app.Run(project, []string{"list"})
	if err != nil {
		t.Fatalf("list before inventory: %v", err)
	}
	if !strings.Contains(legacyList, "AGENTS.md\n") || !strings.Contains(legacyList, ".agents/skills/review/references/checklist.md\n") {
		t.Fatalf("legacy list omitted generated paths: %s", legacyList)
	}

	var first string
	for i := 0; i < 3; i++ {
		out, err := app.Run(caller, []string{"inventory", project})
		if err != nil {
			t.Fatalf("inventory run %d: %v", i, err)
		}
		if i == 0 {
			first = out
		} else if out != first {
			t.Fatalf("inventory output changed between runs:\n%s\n%s", first, out)
		}
	}
	doc := decodeInventory(t, first)
	if doc.Status != "ok" || len(doc.Diagnostics) != 0 {
		t.Fatalf("unexpected inventory status: %+v", doc)
	}
	wantResources := []string{
		"shared_instruction:project", "agent_instruction:codex", "path_rule:alpha", "path_rule:zeta",
		"agent:reviewer", "skill:review", "mcp_server:alpha", "mcp_server:zeta",
		"plugin:alpha@tools", "plugin:zeta@tools", "hook_event:SessionStart", "setup_entry:tool",
	}
	var gotResources []string
	for _, resource := range doc.Resources {
		gotResources = append(gotResources, resource.Type+":"+resource.Name)
		for _, path := range append(append([]string{}, resource.Sources...), resource.Destinations...) {
			if filepath.IsAbs(path) || strings.Contains(path, "\\") {
				t.Fatalf("non-portable resource path %q", path)
			}
		}
	}
	if !reflect.DeepEqual(gotResources, wantResources) {
		t.Fatalf("resource order: got %v, want %v", gotResources, wantResources)
	}
	if !reflect.DeepEqual(doc.Resources[5].Sources, []string{".ai/skills/review/SKILL.md", ".ai/skills/review/references/checklist.md"}) {
		t.Fatalf("skill auxiliaries missing: %+v", doc.Resources[5])
	}
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/assets/a-first.txt", ".ai/z-last.txt"}) {
		t.Fatalf("unclaimed files: %v", doc.AdditionalFiles)
	}
	if after := snapshotProject(t, project); !reflect.DeepEqual(after, before) {
		t.Fatalf("inventory changed project tree:\nbefore: %v\nafter: %v", before, after)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inventory ran a declared command: %v", err)
	}
	if afterList, err := app.Run(project, []string{"list"}); err != nil || afterList != legacyList {
		t.Fatalf("legacy list changed: %v\nbefore: %s\nafter: %s", err, legacyList, afterList)
	}

	writeFile(t, project, ".ai/hooks.yaml", "ZBroken:\n  - matcher: Write\nABroken:\n  - matcher: Read\nSessionStart:\n  - hooks:\n      - type: command\n        command: touch "+marker+"\n")
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: org/tools\n  zulu:\n    source: github\nplugins:\n  zeta@tools: false\n  alpha@tools: true\n  alpha: true\n")
	writeFile(t, project, ".ai/rules/broken.md", "---\npaths: [\n")
	invalidBefore := snapshotProject(t, project)
	invalid, err := app.Run(caller, []string{"inventory", project})
	if err == nil {
		t.Fatal("recoverable errors should make inventory invalid")
	}
	bad := decodeInventory(t, invalid)
	if bad.Status != "invalid" || len(bad.Resources) != len(wantResources) || !reflect.DeepEqual(bad.AdditionalFiles, []string{".ai/assets/a-first.txt", ".ai/rules/broken.md", ".ai/z-last.txt"}) {
		t.Fatalf("invalid source hid readable resources or unclaimed files: %+v", bad)
	}
	if len(bad.Diagnostics) != 5 || bad.Diagnostics[0].Path != ".ai/hooks.yaml" || bad.Diagnostics[0].Unit != "ABroken" || bad.Diagnostics[1].Unit != "ZBroken" || bad.Diagnostics[2].Path != ".ai/plugins.yaml" || bad.Diagnostics[2].Unit != "alpha" || bad.Diagnostics[3].Unit != "zulu" || bad.Diagnostics[4].Path != ".ai/rules/broken.md" {
		t.Fatalf("diagnostics are not ordered by path and unit: %+v", bad.Diagnostics)
	}
	if repeated, err := app.Run(caller, []string{"inventory", project}); err == nil || repeated != invalid {
		t.Fatalf("invalid inventory output changed: %v\n%s\n%s", err, invalid, repeated)
	}
	for _, diagnostic := range bad.Diagnostics {
		if filepath.IsAbs(diagnostic.Path) || strings.Contains(diagnostic.Path, "\\") {
			t.Fatalf("non-portable diagnostic path %q", diagnostic.Path)
		}
	}
	if after := snapshotProject(t, project); !reflect.DeepEqual(after, invalidBefore) {
		t.Fatalf("invalid inventory changed project tree:\nbefore: %v\nafter: %v", invalidBefore, after)
	}

	missingProject := t.TempDir()
	missing, err := app.Run(caller, []string{"inventory", missingProject})
	if err == nil || decodeInventory(t, missing).Status != "missing" {
		t.Fatalf("missing source status: %v, %s", err, missing)
	}
	writeFile(t, missingProject, ".ai", "not a directory")
	invalidRoot, err := app.Run(caller, []string{"inventory", missingProject})
	if err == nil || decodeInventory(t, invalidRoot).Status != "invalid" {
		t.Fatalf("invalid source status: %v, %s", err, invalidRoot)
	}
}
