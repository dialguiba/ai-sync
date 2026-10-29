package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogTUITestProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	files := map[string]string{
		".ai/project.md":                 "# Project\n",
		".ai/mcp.yaml":                   "servers: {}\n",
		".ai/skills/quick-help/SKILL.md": "---\nname: quick-help\ndescription: Help with tasks.\n---\n\n# Quick help\n",
	}
	for path, contents := range files {
		full := filepath.Join(project, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return project
}

func TestCatalogTUISearchAndInspectSkill(t *testing.T) {
	project := catalogTUITestProject(t)
	var output bytes.Buffer
	if err := runCatalogTUI(project, strings.NewReader("/\nquick-help\n1\nb\nq\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Resources (1/", "quick-help", ".ai/skills/quick-help/SKILL.md", "Generated destinations:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("TUI missing %q:\n%s", want, output.String())
		}
	}
}

func TestCatalogTUICopySkillUsesReviewedPlan(t *testing.T) {
	project := catalogTUITestProject(t)
	target := t.TempDir()
	response, err := json.Marshal(map[string]any{"result": map[string]any{"worktrees": []map[string]string{{
		"id": "target", "path": target, "displayName": "target", "hostId": "local",
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '" + string(response) + "'\n"
	if err := os.WriteFile(filepath.Join(bin, orcaCLICommand()), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	raw, err := inventoryProject(project, []string{project})
	if err != nil {
		t.Fatal(err)
	}
	var inventory inventoryResult
	if err := json.Unmarshal([]byte(raw), &inventory); err != nil {
		t.Fatal(err)
	}
	var skill inventoryResource
	for _, item := range inventory.Resources {
		if item.Type == "skill" && item.Name == "quick-help" {
			skill = item
		}
	}
	if skill.Name == "" {
		t.Fatalf("test skill absent from inventory: %s", raw)
	}
	var output bytes.Buffer
	message, err := catalogTUICopy(project, skill, bufio.NewReader(strings.NewReader("1\nr\ny\n")), &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "Copied 1 files") || !strings.Contains(output.String(), "Copy preview: 1 files") {
		t.Fatalf("unexpected copy result: %s\n%s", message, output.String())
	}
	contents, err := os.ReadFile(filepath.Join(target, ".ai/skills/quick-help/SKILL.md"))
	if err != nil || !strings.Contains(string(contents), "# Quick help") {
		t.Fatal(fmt.Errorf("copied skill unavailable: %w", err))
	}
}

func TestCatalogTUISearchesPluginComponents(t *testing.T) {
	entries := []inventoryResource{{Type: "plugin", Name: "toolkit", Variants: []inventoryPluginVariant{{Skills: []string{"incident-review"}}}}}
	visible := catalogTUIFilter(entries, "incident")
	if len(visible) != 1 || visible[0].Name != "toolkit" {
		t.Fatalf("plugin component search missed its parent: %#v", visible)
	}
}
