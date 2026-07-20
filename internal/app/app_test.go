package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func TestHelpShowsCommandsAndOptions(t *testing.T) {
	dir := t.TempDir()

	out, err := app.Run(dir, []string{"--help"})
	if err != nil {
		t.Fatalf("help should not return an error: %v", err)
	}
	for _, want := range []string{
		"keeps AI-agent configuration",
		"Usage:",
		"ai-sync init",
		"ai-sync convention",
		"ai-sync list",
		"ai-sync version",
		"convention        print the .ai authoring convention",
		"list              print generated file paths",
		"version           print version and build metadata",
		"--target claude|codex|kiro",
		"--dry-run",
		"Examples:",
		"See README.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected help to contain %q, got:\n%s", want, out)
		}
	}
}

func TestVersionShowsBuildMetadata(t *testing.T) {
	dir := t.TempDir()

	info := app.BuildInfo{Version: "v1.2.3", Commit: "abc123", Date: "2026-07-19T00:00:00Z"}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		out, err := app.RunWithBuildInfo(dir, args, info)
		if err != nil {
			t.Fatalf("version should not return an error for args %v: %v", args, err)
		}
		for _, want := range []string{"ai-sync v1.2.3", "commit abc123", "built 2026-07-19T00:00:00Z"} {
			if !strings.Contains(out, want) {
				t.Fatalf("expected version output for args %v to contain %q, got:\n%s", args, want, out)
			}
		}
	}
}

func TestConventionShowsCanonicalAuthoringGuide(t *testing.T) {
	dir := t.TempDir()

	out, err := app.Run(dir, []string{"convention"})
	if err != nil {
		t.Fatalf("convention should not return an error: %v", err)
	}
	for _, want := range []string{
		"# ai-sync .ai Authoring Convention",
		"Create a canonical .ai/ directory",
		".ai/project.md",
		".ai/mcp.yaml",
		".ai/targets/claude.md",
		".ai/targets/codex.md",
		".ai/targets/kiro.md",
		".ai/rules/<name>.md",
		"paths list in YAML frontmatter",
		".ai/skills/<name>/SKILL.md",
		"Do not create generated agent files directly",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected convention output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestInitCreatesCanonicalSource(t *testing.T) {
	dir := t.TempDir()

	out, err := app.Run(dir, []string{"init"})
	if err != nil {
		t.Fatalf("Run init returned error: %v", err)
	}
	if !strings.Contains(out, "created .ai") {
		t.Fatalf("expected init output to mention created .ai, got %q", out)
	}

	for _, path := range []string{
		".ai/project.md",
		".ai/mcp.yaml",
		".ai/targets/claude.md",
		".ai/targets/codex.md",
		".ai/targets/kiro.md",
		".ai/rules/example.md",
		".ai/skills/example/SKILL.md",
	} {
		assertFileExists(t, filepath.Join(dir, path))
	}
}

func TestListHelpReturnsSuccess(t *testing.T) {
	dir := t.TempDir()

	out, err := app.Run(dir, []string{"list", "--help"})
	if err != nil {
		t.Fatalf("list help should not return an error: %v", err)
	}
	for _, want := range []string{
		"Usage of ai-sync list",
		"target to list: claude, codex, or kiro",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected list help to contain %q, got:\n%s", want, out)
		}
	}
}

func TestListShowsGeneratedFilesWithoutWritingThem(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	out, err := app.Run(dir, []string{"list"})
	if err != nil {
		t.Fatalf("list should not return an error: %v", err)
	}
	for _, want := range []string{
		"CLAUDE.md",
		".claude/settings.json",
		".mcp.json",
		".claude/skills/playwright-cli/SKILL.md",
		"AGENTS.md",
		".codex/config.toml",
		".agents/skills/playwright-cli/SKILL.md",
		".kiro/steering/project-conventions.md",
		".kiro/settings/mcp.json",
		".kiro/powers/playwright-cli/POWER.md",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("expected list output to contain %q as a line, got:\n%s", want, out)
		}
	}
	assertFileMissing(t, filepath.Join(dir, "CLAUDE.md"))
	assertFileMissing(t, filepath.Join(dir, "AGENTS.md"))
}

func TestListIncludesNativePathRuleOutputs(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---

# Frontend Rules
`)

	out, err := app.Run(dir, []string{"list"})
	if err != nil {
		t.Fatalf("list should not return an error: %v", err)
	}
	for _, want := range []string{
		".claude/rules/.ai-sync-manifest\n",
		".claude/rules/frontend.md\n",
		".codex/scoped-agents-manifest\n",
		"frontend/AGENTS.md\n",
		".kiro/steering/.ai-sync-manifest\n",
		".kiro/steering/frontend.md\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected list output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestListSupportsTargetFlag(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	out, err := app.Run(dir, []string{"list", "--target", "codex"})
	if err != nil {
		t.Fatalf("targeted list should not return an error: %v", err)
	}
	for _, want := range []string{
		"AGENTS.md\n",
		".codex/config.toml\n",
		".agents/skills/playwright-cli/SKILL.md\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected targeted list to contain %q, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"CLAUDE.md\n",
		".kiro/steering/project-conventions.md\n",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected targeted list not to contain %q, got:\n%s", unwanted, out)
		}
	}
}

func TestSyncWarnsBeforeOverwritingUnmarkedExistingOutput(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, "AGENTS.md", "# Manual Codex Instructions\n")

	out, err := app.Run(dir, []string{"--target", "codex"})
	if err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}
	if !strings.Contains(out, "warning: overwriting existing unmarked file AGENTS.md") {
		t.Fatalf("expected overwrite warning, got:\n%s", out)
	}
	if !strings.Contains(out, "wrote AGENTS.md") {
		t.Fatalf("expected sync to continue writing AGENTS.md, got:\n%s", out)
	}
}

func TestSyncDoesNotWarnWhenOverwritingMarkedGeneratedOutput(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	if _, err := app.Run(dir, []string{"--target", "codex"}); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	writeFile(t, dir, ".ai/project.md", "# Project Rules\n\nUpdated guidance.\n")

	out, err := app.Run(dir, []string{"--target", "codex"})
	if err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}
	if strings.Contains(out, "warning: overwriting existing unmarked file AGENTS.md") {
		t.Fatalf("expected no warning for marked generated AGENTS.md, got:\n%s", out)
	}
	if !strings.Contains(out, "wrote AGENTS.md") {
		t.Fatalf("expected AGENTS.md to be updated, got:\n%s", out)
	}
}

func TestSyncDoesNotWarnWhenOverwritingMarkedFrontmatterRuleOutput(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---

# Frontend Rules

Use atomic design.
`)

	if _, err := app.Run(dir, []string{"--target", "claude"}); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---

# Frontend Rules

Use atomic design and container components.
`)

	out, err := app.Run(dir, []string{"--target", "claude"})
	if err != nil {
		t.Fatalf("sync should not return an error: %v", err)
	}
	if strings.Contains(out, "warning: overwriting existing unmarked file .claude/rules/frontend.md") {
		t.Fatalf("expected no warning for marked frontmatter rule, got:\n%s", out)
	}
	if !strings.Contains(out, "wrote .claude/rules/frontend.md") {
		t.Fatalf("expected Claude rule to be updated, got:\n%s", out)
	}
}

func TestSyncGeneratesClaudeCodexAndKiroOutputs(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	out, err := app.Run(dir, nil)
	if err != nil {
		t.Fatalf("Run sync returned error: %v", err)
	}
	if !strings.Contains(out, "wrote CLAUDE.md") || !strings.Contains(out, "wrote AGENTS.md") || !strings.Contains(out, "wrote .kiro/steering/project-conventions.md") {
		t.Fatalf("expected output to list generated files, got %q", out)
	}

	assertFileContains(t, filepath.Join(dir, "CLAUDE.md"), "Use conventional commits")
	assertFileContains(t, filepath.Join(dir, "CLAUDE.md"), "Claude-specific guidance")
	assertFileContains(t, filepath.Join(dir, "AGENTS.md"), "Codex-specific guidance")
	assertFileContains(t, filepath.Join(dir, ".kiro/steering/project-conventions.md"), "Kiro-specific guidance")

	assertFileContains(t, filepath.Join(dir, ".mcp.json"), "playwright")
	assertFileContains(t, filepath.Join(dir, ".codex/config.toml"), `[mcp_servers.playwright]`)
	assertFileContains(t, filepath.Join(dir, ".kiro/settings/mcp.json"), "mcpServers")

	assertFileContains(t, filepath.Join(dir, ".claude/skills/playwright-cli/SKILL.md"), "Playwright CLI")
	assertFileContains(t, filepath.Join(dir, ".agents/skills/playwright-cli/SKILL.md"), "Playwright CLI")
	kiroPower, err := os.ReadFile(filepath.Join(dir, ".kiro/powers/playwright-cli/POWER.md"))
	if err != nil {
		t.Fatalf("read generated Kiro Power: %v", err)
	}
	if !strings.HasPrefix(string(kiroPower), "---\nname: \"playwright-cli\"") {
		t.Fatalf("expected Kiro Power frontmatter to stay at file start, got:\n%s", string(kiroPower))
	}
	assertFileContains(t, filepath.Join(dir, ".kiro/powers/playwright-cli/POWER.md"), "Generated by ai-sync")
}

func TestClaudeUsesNativePathScopedRules(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
  - "src/**/*.tsx"
---

# Frontend Rules

Use atomic design for UI components.
`)

	if _, err := app.Run(dir, []string{"--target", "claude"}); err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}

	rulePath := filepath.Join(dir, ".claude/rules/frontend.md")
	assertFileContains(t, rulePath, "---\npaths:\n  - \"frontend/**\"\n  - \"src/**/*.tsx\"\n---")
	assertFileContainsInOrder(t, rulePath, []string{
		"## Scope",
		"These rules are path-scoped. Apply the instructions below only to files matching: `frontend/**`, `src/**/*.tsx`.",
		"Use atomic design for UI components.",
	})
	assertFileContains(t, rulePath, "Use atomic design for UI components.")
	assertFileNotContains(t, filepath.Join(dir, "CLAUDE.md"), "## Path-Scoped Rules")
}

func TestKiroUsesNativeFileMatchSteeringRules(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
  - "src/**/*.tsx"
---

# Frontend Rules

Use atomic design for UI components.
`)

	if _, err := app.Run(dir, []string{"--target", "kiro"}); err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}

	rulePath := filepath.Join(dir, ".kiro/steering/frontend.md")
	assertFileContains(t, rulePath, "---\ninclusion: fileMatch\nfileMatchPattern: [\"frontend/**\", \"src/**/*.tsx\"]\n---")
	assertFileContainsInOrder(t, rulePath, []string{
		"## Scope",
		"These rules are path-scoped. Apply the instructions below only to files matching: `frontend/**`, `src/**/*.tsx`.",
		"Use atomic design for UI components.",
	})
	assertFileContains(t, rulePath, "Use atomic design for UI components.")
	assertFileNotContains(t, filepath.Join(dir, ".kiro/steering/project-conventions.md"), "## Path-Scoped Rules")
}

func TestCodexUsesNestedAgentsForPathScopedRules(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
  - "src/**/*.tsx"
---

# Frontend Rules

Use atomic design for UI components.
`)

	if _, err := app.Run(dir, []string{"--target", "codex"}); err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}

	rootAgentsPath := filepath.Join(dir, "AGENTS.md")
	assertFileContains(t, rootAgentsPath, "# Codex Agent Instructions")
	assertFileNotContains(t, rootAgentsPath, "## Path-Scoped Rules")

	for _, rel := range []string{"frontend/AGENTS.md", "src/AGENTS.md"} {
		rulePath := filepath.Join(dir, rel)
		assertFileContains(t, rulePath, "# Codex Path-Scoped Agent Instructions")
		assertFileContainsInOrder(t, rulePath, []string{
			"## Scope",
			"This AGENTS.md applies only to files in this directory tree.",
			"Apply each rule below only to files matching its listed globs.",
			"### frontend",
			"Use atomic design for UI components.",
		})
		assertFileContains(t, rulePath, "### frontend")
		assertFileContains(t, rulePath, "Applies to: `frontend/**`, `src/**/*.tsx`")
		assertFileContains(t, rulePath, "Use atomic design for UI components.")
	}
	assertFileContains(t, filepath.Join(dir, ".codex/scoped-agents-manifest"), "frontend/AGENTS.md")
	assertFileContains(t, filepath.Join(dir, ".codex/scoped-agents-manifest"), "src/AGENTS.md")
}

func TestCodexRootPathScopedRulesWarnAboutScope(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/go.md", `---
paths:
  - "*.go"
---

# Go Rules

Use table-driven tests.
`)

	if _, err := app.Run(dir, []string{"--target", "codex"}); err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}

	rootAgentsPath := filepath.Join(dir, "AGENTS.md")
	assertFileContainsInOrder(t, rootAgentsPath, []string{
		"## Path-Scoped Rules",
		"## Scope",
		"The rules below are path-scoped. Apply each rule only to files matching its listed globs.",
		"### go",
		"Applies to: `*.go`",
		"Use table-driven tests.",
	})
}

func TestSyncIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	first := snapshotFiles(t, dir, []string{
		"CLAUDE.md",
		"AGENTS.md",
		".mcp.json",
		".codex/config.toml",
		".kiro/settings/mcp.json",
		".kiro/powers/playwright-cli/POWER.md",
		".kiro/powers/playwright-cli/.ai-sync-manifest",
	})

	out, err := app.Run(dir, nil)
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	if !strings.Contains(out, "no changes") {
		t.Fatalf("expected no changes output, got %q", out)
	}
	second := snapshotFiles(t, dir, []string{
		"CLAUDE.md",
		"AGENTS.md",
		".mcp.json",
		".codex/config.toml",
		".kiro/settings/mcp.json",
		".kiro/powers/playwright-cli/POWER.md",
		".kiro/powers/playwright-cli/.ai-sync-manifest",
	})
	if first != second {
		t.Fatalf("expected second sync to preserve generated bytes")
	}
}

func TestSyncPrunesStaleGeneratedSkillOutputs(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".ai/skills/playwright-cli")); err != nil {
		t.Fatalf("remove source skill: %v", err)
	}

	out, err := app.Run(dir, nil)
	if err != nil {
		t.Fatalf("sync after skill removal failed: %v", err)
	}
	if !strings.Contains(out, "removed .agents/skills/playwright-cli/SKILL.md") || !strings.Contains(out, "removed .kiro/powers/playwright-cli/POWER.md") {
		t.Fatalf("expected stale generated skill files to be pruned, got %q", out)
	}
	assertFileMissing(t, filepath.Join(dir, ".claude/skills/playwright-cli"))
	assertFileMissing(t, filepath.Join(dir, ".agents/skills/playwright-cli"))
	assertFileMissing(t, filepath.Join(dir, ".kiro/powers/playwright-cli"))
}

func TestPruneRemovesStaleGeneratedPathRules(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---

# Frontend Rules
`)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".ai/rules/frontend.md")); err != nil {
		t.Fatalf("remove source rule: %v", err)
	}

	out, err := app.Run(dir, nil)
	if err != nil {
		t.Fatalf("sync after rule removal failed: %v", err)
	}
	for _, want := range []string{
		"removed .claude/rules/frontend.md",
		"removed frontend/AGENTS.md",
		"removed .kiro/steering/frontend.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected stale generated rules to include %q, got %q", want, out)
		}
	}
	assertFileMissing(t, filepath.Join(dir, ".claude/rules/frontend.md"))
	assertFileMissing(t, filepath.Join(dir, ".claude/rules/.ai-sync-manifest"))
	assertFileMissing(t, filepath.Join(dir, "frontend/AGENTS.md"))
	assertFileMissing(t, filepath.Join(dir, ".codex/scoped-agents-manifest"))
	assertFileMissing(t, filepath.Join(dir, ".kiro/steering/frontend.md"))
}

func TestPrunePreservesUserOwnedFilesInRuleDirs(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---

# Frontend Rules
`)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	writeFile(t, dir, ".claude/rules/manual.md", "# Manual Claude Rule\n")
	writeFile(t, dir, ".kiro/steering/manual.md", "# Manual Kiro Steering\n")
	if err := os.Remove(filepath.Join(dir, ".ai/rules/frontend.md")); err != nil {
		t.Fatalf("remove source rule: %v", err)
	}

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("sync after rule removal failed: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, ".claude/rules/manual.md"))
	assertFileExists(t, filepath.Join(dir, ".kiro/steering/manual.md"))
}

func TestPrunePreservesUserOwnedFilesInGeneratedSkillDirs(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	writeFile(t, dir, ".agents/skills/playwright-cli/user-notes.md", "do not delete\n")
	if err := os.RemoveAll(filepath.Join(dir, ".ai/skills/playwright-cli")); err != nil {
		t.Fatalf("remove source skill: %v", err)
	}

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("sync after skill removal failed: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, ".agents/skills/playwright-cli/user-notes.md"))
	assertFileMissing(t, filepath.Join(dir, ".agents/skills/playwright-cli/SKILL.md"))
}

func TestPruneIgnoresManualSkillDirsWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".agents/skills/manual/SKILL.md", "# Manual\n")

	if _, err := app.Run(dir, []string{"--target", "codex"}); err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}
	assertFileExists(t, filepath.Join(dir, ".agents/skills/manual/SKILL.md"))
}

func TestDryRunPruneDoesNotDeleteFiles(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".ai/skills/playwright-cli")); err != nil {
		t.Fatalf("remove source skill: %v", err)
	}

	out, err := app.Run(dir, []string{"--dry-run"})
	if err != nil {
		t.Fatalf("dry-run prune failed: %v", err)
	}
	if !strings.Contains(out, "would remove .agents/skills/playwright-cli/SKILL.md") {
		t.Fatalf("expected dry-run prune output, got %q", out)
	}
	assertFileExists(t, filepath.Join(dir, ".agents/skills/playwright-cli/SKILL.md"))
}

func TestTargetFlagOnlyGeneratesSelectedTarget(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	out, err := app.Run(dir, []string{"--target", "codex"})
	if err != nil {
		t.Fatalf("targeted sync failed: %v", err)
	}
	if !strings.Contains(out, "wrote AGENTS.md") {
		t.Fatalf("expected codex output, got %q", out)
	}
	assertFileExists(t, filepath.Join(dir, "AGENTS.md"))
	assertFileMissing(t, filepath.Join(dir, "CLAUDE.md"))
	assertFileMissing(t, filepath.Join(dir, ".kiro/steering/project-conventions.md"))
}

func TestDryRunDoesNotWriteFiles(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	out, err := app.Run(dir, []string{"--dry-run"})
	if err != nil {
		dryRunReturnedError := err
		t.Fatalf("dry run failed: %v", dryRunReturnedError)
	}
	if !strings.Contains(out, "would write CLAUDE.md") {
		t.Fatalf("expected dry-run output, got %q", out)
	}
	assertFileMissing(t, filepath.Join(dir, "CLAUDE.md"))
	assertFileMissing(t, filepath.Join(dir, "AGENTS.md"))
}

func TestUnknownTargetReturnsActionableError(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)

	_, err := app.Run(dir, []string{"--target", "junie"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "expected claude, codex, or kiro") {
		t.Fatalf("expected actionable target error, got %v", err)
	}
}

func TestPathScopedRuleRequiresPathsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", "# Frontend Rules\n\nUse atomic design.\n")

	_, err := app.Run(dir, []string{"--target", "codex"})
	if err == nil {
		t.Fatal("expected missing frontmatter error")
	}
	if !strings.Contains(err.Error(), "parse .ai/rules/frontend.md") || !strings.Contains(err.Error(), "missing YAML frontmatter") {
		t.Fatalf("expected actionable path rule parse error, got %v", err)
	}
}

func TestPathScopedRuleRejectsMalformedClosingFrontmatterDelimiter(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/rules/frontend.md", `---
paths:
  - "frontend/**"
---oops

# Frontend Rules

Use atomic design.
`)

	_, err := app.Run(dir, []string{"--target", "codex"})
	if err == nil {
		t.Fatal("expected malformed closing delimiter error")
	}
	if !strings.Contains(err.Error(), "parse .ai/rules/frontend.md") || !strings.Contains(err.Error(), "missing closing frontmatter delimiter") {
		t.Fatalf("expected actionable path rule parse error, got %v", err)
	}
}

func TestMalformedMCPYAMLReturnsParseError(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/mcp.yaml", "servers:\n  broken: [\n")

	_, err := app.Run(dir, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse .ai/mcp.yaml") {
		t.Fatalf("expected MCP parse error, got %v", err)
	}
}

func TestSyncWithoutCanonicalSourceReturnsActionableError(t *testing.T) {
	dir := t.TempDir()

	_, err := app.Run(dir, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "run `ai-sync init`") {
		t.Fatalf("expected actionable init hint, got %v", err)
	}
}

func writeCanonicalSource(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, ".ai/project.md", "# Project\n\nUse conventional commits.\n")
	writeFile(t, dir, ".ai/targets/claude.md", "# Claude\n\nClaude-specific guidance.\n")
	writeFile(t, dir, ".ai/targets/codex.md", "# Codex\n\nCodex-specific guidance.\n")
	writeFile(t, dir, ".ai/targets/kiro.md", "# Kiro\n\nKiro-specific guidance.\n")
	writeFile(t, dir, ".ai/mcp.yaml", `servers:
  playwright:
    command: npx
    args:
      - -y
      - '@playwright/mcp@latest'
    env:
      PLAYWRIGHT_HEADLESS: "true"
`)
	writeFile(t, dir, ".ai/skills/playwright-cli/SKILL.md", `---
name: "playwright-cli"
description: "Use Playwright from the CLI."
---

# Playwright CLI

Use Playwright for browser checks.
`)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file %s to be missing, stat err=%v", path, err)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(contents), want) {
		t.Fatalf("expected %s to contain %q, got:\n%s", path, want, string(contents))
	}
}

func assertFileNotContains(t *testing.T, path, unwanted string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(contents), unwanted) {
		t.Fatalf("expected %s not to contain %q, got:\n%s", path, unwanted, string(contents))
	}
}

func assertFileContainsInOrder(t *testing.T, path string, wants []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(contents)
	offset := 0
	for _, want := range wants {
		index := strings.Index(text[offset:], want)
		if index < 0 {
			t.Fatalf("expected %s to contain %q after byte %d, got:\n%s", path, want, offset, text)
		}
		offset += index + len(want)
	}
}

func snapshotFiles(t *testing.T, root string, rels []string) string {
	t.Helper()
	var b strings.Builder
	for _, rel := range rels {
		contents, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		b.WriteString("--- ")
		b.WriteString(rel)
		b.WriteString("\n")
		b.Write(contents)
		b.WriteString("\n")
	}
	return b.String()
}
