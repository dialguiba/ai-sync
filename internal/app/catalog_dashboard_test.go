package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func dashboardTestModel(t *testing.T) *catalogDashboard {
	t.Helper()
	project := catalogTUITestProject(t)
	m := &catalogDashboard{current: project, active: project, width: 80, height: 24, mode: "catalog"}
	if err := m.loadInventory(); err != nil {
		t.Fatal(err)
	}
	return m
}

func dashboardClick(t *testing.T, m *catalogDashboard, action string) tea.Cmd {
	t.Helper()
	m.View()
	for _, hit := range m.hits {
		if hit.action == action {
			_, cmd := m.Update(tea.MouseMsg{X: hit.x1 + 1, Y: hit.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			return cmd
		}
	}
	t.Fatalf("click target %q is not visible in mode %s", action, m.mode)
	return nil
}

func TestDashboardRendersWithinTerminalAndMouseSelectsResource(t *testing.T) {
	m := dashboardTestModel(t)
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Fatalf("rendered %d lines for a 24-line terminal", len(lines))
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width != 80 {
			t.Fatalf("line %d is %d cells wide, want 80", i, width)
		}
	}
	for i, item := range m.visible {
		if item.Type == "skill" && item.Name == "quick-help" {
			dashboardClick(t, m, "resource:"+strconv.Itoa(i))
			if selected, ok := m.selectedItem(); !ok || selected.Name != "quick-help" {
				t.Fatalf("mouse selected %#v", selected)
			}
			return
		}
	}
	t.Fatal("skill missing from dashboard")
}

func TestDashboardCopyFlowUsesClickableReviewedPlan(t *testing.T) {
	m := dashboardTestModel(t)
	for i, item := range m.visible {
		if item.Type == "skill" && item.Name == "quick-help" {
			m.selected = i
		}
	}
	target := t.TempDir()
	response, _ := json.Marshal(map[string]any{"result": map[string]any{"worktrees": []map[string]string{{
		"id": "target", "path": target, "displayName": "target", "hostId": "local",
	}}}})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, orcaCLICommand()), []byte("#!/bin/sh\nprintf '%s\\n' '"+string(response)+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd := dashboardClick(t, m, "copy")
	if cmd == nil {
		t.Fatal("copy did not load destination worktrees")
	}
	m.Update(cmd())
	if m.mode != "targets" {
		t.Fatalf("mode after loading destinations: %s (%s)", m.mode, m.message)
	}
	dashboardClick(t, m, "target:0")
	cmd = dashboardClick(t, m, "scope:resource")
	m.Update(cmd())
	if m.mode != "review" || len(m.plan.Files) != 1 {
		t.Fatalf("copy preview unavailable: mode %s, files %d", m.mode, len(m.plan.Files))
	}
	m.activate("filter:plugin")
	if m.copyItem.Name != "quick-help" || m.kindFilter != "" {
		t.Fatal("catalog filter changed the resource during copy review")
	}
	dashboardClick(t, m, "confirm")
	cmd = dashboardClick(t, m, "apply")
	m.Update(cmd())
	if m.mode != "catalog" || !strings.Contains(m.message, "Copied 1 files") {
		t.Fatalf("copy result: mode %s, message %s", m.mode, m.message)
	}
	if _, err := os.Stat(filepath.Join(target, ".ai/skills/quick-help/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardAddPluginFormWritesCanonicalSource(t *testing.T) {
	m := dashboardTestModel(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dashboardClick(t, m, "add-plugin")
	for _, value := range []string{"kit@tools", "repo", "org/tools"} {
		for _, r := range value {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	if m.mode != "plugin-confirm" || m.pendingAction != "add" {
		t.Fatalf("add form did not reach confirmation: mode=%s, stage=%d, message=%s", m.mode, m.formStage, m.message)
	}
	cmd := dashboardClick(t, m, "plugin-execute")
	m.Update(cmd())
	contents, err := os.ReadFile(filepath.Join(m.active, ".ai/plugins.yaml"))
	if err != nil || !strings.Contains(string(contents), "kit@tools: true") {
		t.Fatalf("plugin not written to canonical source: %s, %v", contents, err)
	}
}
