package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func TestSyncKeepsTheExecutableBitOfSkillFiles(t *testing.T) {
	dir := t.TempDir()
	writeCanonicalSource(t, dir)
	writeFile(t, dir, ".ai/skills/tool/SKILL.md", "---\nname: tool\ndescription: t\n---\nRun scripts/run.\n")
	writeFile(t, dir, ".ai/skills/tool/scripts/run", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, ".ai/skills/tool/scripts/run"), 0o755); err != nil {
		t.Fatal(err)
	}
	outputs := []string{".claude/skills/tool/scripts/run", ".agents/skills/tool/scripts/run", ".kiro/skills/tool/scripts/run"}

	if _, err := app.Run(dir, nil); err != nil {
		t.Fatal(err)
	}
	for _, rel := range outputs {
		assertExecutable(t, filepath.Join(dir, rel))
	}

	for _, rel := range outputs {
		if err := os.Chmod(filepath.Join(dir, rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := app.Run(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range outputs {
		assertExecutable(t, filepath.Join(dir, rel))
	}
	if out == "no changes\n" {
		t.Fatal("restoring a lost executable bit was not reported")
	}
}

func assertExecutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s lost its executable bit: %v", path, info.Mode())
	}
}
