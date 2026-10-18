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
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inventory ran plugin installer: %v", err)
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
	if !reflect.DeepEqual(doc.AdditionalFiles, []string{".ai/notes.txt", ".ai/setup.yaml"}) {
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
	before := snapshotProjectTree(t, project)

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
	if after := snapshotProjectTree(t, project); !reflect.DeepEqual(after, before) {
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
			before := snapshotProjectTree(t, project)

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
			if after := snapshotProjectTree(t, project); !reflect.DeepEqual(after, before) {
				t.Fatalf("inventory changed project tree:\nbefore: %v\nafter: %v", before, after)
			}
		})
	}
}

func snapshotProjectTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
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
		t.Fatalf("snapshot project tree: %v", err)
	}
	return files
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
	if skill.Type != "skill" || skill.Name != "review" || !reflect.DeepEqual(skill.Sources, []string{
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
