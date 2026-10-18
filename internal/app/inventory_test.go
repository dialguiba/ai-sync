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
	Status    string `json:"status"`
	Resources []struct {
		Type         string   `json:"type"`
		Name         string   `json:"name"`
		Sources      []string `json:"sources"`
		Destinations []string `json:"destinations"`
	} `json:"resources"`
	AdditionalFiles []string `json:"additional_files"`
	Diagnostics     []struct {
		Path    string `json:"path"`
		Message string `json:"message"`
	} `json:"diagnostics"`
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
	if len(doc.Resources) != 4 {
		t.Fatalf("expected shared and three target instructions, got %+v", doc.Resources)
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
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/mcp.yaml", ".ai/notes.txt", ".ai/setup.yaml"}) {
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
