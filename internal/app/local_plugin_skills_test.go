package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func writeLocalMarketplace(t *testing.T, dir string) {
	t.Helper()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/plugins.yaml", `marketplaces:
  local:
    path: ./.ai/plugins-local
plugins:
  demo@local: true
`)
	writeFile(t, dir, ".ai/plugins-local/.claude-plugin/marketplace.json",
		`{"name":"local","owner":{"name":"x"},"plugins":[{"name":"demo","source":"./demo"}]}`)
	writeFile(t, dir, ".ai/plugins-local/demo/plugin.json", `{"name":"demo"}`)
	writeFile(t, dir, ".ai/plugins-local/demo/skills/hello/SKILL.md", "---\nname: hello\ndescription: hi\n---\nhi\n")
	writeFile(t, dir, ".ai/plugins-local/demo/skills/hello/references/guide.md", "guide\n")
	writeFile(t, dir, ".ai/plugins-local/demo/agents/helper.md", "---\nname: helper\n---\nhelper body\n")
}

func TestSyncCopiesLocalPluginSkillsToCodexAndKiroOnly(t *testing.T) {
	dir := t.TempDir()
	writeLocalMarketplace(t, dir)
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}

	for _, base := range []string{".agents/skills", ".kiro/skills"} {
		assertFileContainsInOrder(t, filepath.Join(dir, base, "demo-hello/SKILL.md"), []string{"name: demo-hello"})
		assertFileContainsInOrder(t, filepath.Join(dir, base, "demo-hello/references/guide.md"), []string{"guide"})
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/demo-hello")); err == nil {
		t.Fatal("Claude loads plugin skills through the plugin, so they must not be copied into .claude/skills")
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents/skills/helper")); err == nil {
		t.Fatal("plugin agents are Claude-only and must not be copied")
	}
}

func TestSyncDropsLocalPluginSkillsWhenThePluginIsDisabled(t *testing.T) {
	dir := t.TempDir()
	writeLocalMarketplace(t, dir)
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")
	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	writeFile(t, dir, ".ai/plugins.yaml", strings.Replace(readFile(t, dir, ".ai/plugins.yaml"), "demo@local: true", "demo@local: false", 1))
	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".agents/skills/demo-hello/SKILL.md")); err == nil {
		t.Fatal("a disabled plugin must stop producing skills")
	}
}

func TestSyncRejectsLocalPluginSkillThatCollidesWithOwnSkill(t *testing.T) {
	dir := t.TempDir()
	writeLocalMarketplace(t, dir)
	writeFile(t, dir, ".ai/skills/demo-hello/SKILL.md", "---\nname: demo-hello\ndescription: own\n---\nown\n")
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")

	_, err := app.Run(dir, nil)

	if err == nil || !strings.Contains(err.Error(), `skill "demo-hello" collides`) {
		t.Fatalf("expected a collision error, got %v", err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestSyncPrefixesLocalPluginSkillsAndRewritesReferencesBetweenThem(t *testing.T) {
	dir := t.TempDir()
	writeLocalMarketplace(t, dir)
	writeFile(t, dir, ".ai/plugins-local/demo/skills/create/SKILL.md", "---\nname: create\ndescription: c\n---\nSee `demo:update`.\n")
	writeFile(t, dir, ".ai/plugins-local/demo/skills/update/SKILL.md",
		"---\nname: update\ndescription: u\n---\nRead `../create/REFERENCE.md`, then run `demo:create` or /demo:${lens}.\n")
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}

	assertFileContainsInOrder(t, filepath.Join(dir, ".agents/skills/demo-update/SKILL.md"), []string{
		"name: demo-update",
		"`../demo-create/REFERENCE.md`",
		"`demo-create`",
		"/demo-${lens}",
	})
	assertFileContainsInOrder(t, filepath.Join(dir, ".kiro/skills/demo-create/SKILL.md"), []string{"`demo-update`"})
}

func TestSyncCopiesTheReferencedPluginAgentIntoTheSkillForCodexAndKiro(t *testing.T) {
	dir := t.TempDir()
	writeLocalMarketplace(t, dir)
	writeFile(t, dir, ".ai/plugins-local/demo/skills/review/SKILL.md",
		"---\nname: review\ndescription: r\n---\nSpawn `demo:helper` with the scope.\n")
	writeFile(t, dir, ".ai/plugins-local/demo/skills/other/SKILL.md",
		"---\nname: other\ndescription: o\n---\nNo agent here, only `demo:helper-extra`.\n")
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}

	for _, base := range []string{".agents/skills", ".kiro/skills"} {
		reference := filepath.Join(dir, base, "demo-review/references/helper.md")
		assertFileContainsInOrder(t, reference, []string{"helper body"})
		assertFileNotContains(t, reference, "name: helper")
		assertFileContainsInOrder(t, filepath.Join(dir, base, "demo-review/SKILL.md"), []string{
			"Spawn `demo-helper` with the scope.",
			"## Outside Claude Code",
			"`demo-helper` uses `references/helper.md`",
			`fork_turns="none"`,
		})
		if _, err := os.Stat(filepath.Join(dir, base, "demo-other/references/helper.md")); err == nil {
			t.Fatalf("%s: a skill that does not mention the agent must not get its instructions", base)
		}
		assertFileNotContains(t, filepath.Join(dir, base, "demo-other/SKILL.md"), "Outside Claude Code")
	}
}
