package app_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

type upstreamRepo struct {
	t   *testing.T
	dir string
}

func newUpstreamRepo(t *testing.T) *upstreamRepo {
	t.Helper()
	repo := &upstreamRepo{t: t, dir: t.TempDir()}
	repo.git("init", "-q", "-b", "main")
	return repo
}

func (r *upstreamRepo) git(args ...string) string {
	r.t.Helper()
	command := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func (r *upstreamRepo) commit(files map[string]string) string {
	r.t.Helper()
	for rel, content := range files {
		writeFile(r.t, r.dir, rel, content)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "change")
	return r.git("rev-parse", "HEAD")
}

func writeSourcesYAML(t *testing.T, project string, repo *upstreamRepo, ref string, skills ...string) {
	t.Helper()
	writeFile(t, project, ".ai/sources.yaml", "sources:\n  pstack:\n    git: "+repo.dir+"\n    ref: "+ref+"\n    skills: ["+strings.Join(skills, ", ")+"]\n")
}

func TestSourcesSyncVendorsListedSkillsAtThePinnedRef(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{
		"skills/alpha/SKILL.md":       "alpha v1\n",
		"skills/alpha/scripts/run.sh": "echo v1\n",
		"skills/beta/SKILL.md":        "beta v1\n",
	})
	repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v2\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")

	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "alpha v1\n" {
		t.Fatalf("skill is not the pinned version: %q", got)
	}
	if got := readFile(t, project, ".ai/skills/alpha/scripts/run.sh"); got != "echo v1\n" {
		t.Fatalf("nested file missing: %q", got)
	}
	if _, err := os.Stat(filepath.Join(project, ".ai/skills/beta")); !os.IsNotExist(err) {
		t.Fatalf("unlisted skill was vendored: %v", err)
	}
	assertFileExists(t, filepath.Join(project, ".ai/sources.lock.yaml"))
}

func TestSourcesSyncKeepsTheExecutableBit(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	writeFile(t, repo.dir, "skills/alpha/SKILL.md", "alpha\n")
	writeFile(t, repo.dir, "skills/alpha/scripts/tool", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(repo.dir, "skills/alpha/scripts/tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo.git("add", "-A")
	repo.git("commit", "-q", "-m", "init")
	writeSourcesYAML(t, project, repo, repo.git("rev-parse", "HEAD"), "alpha")

	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(project, ".ai/skills/alpha/scripts/tool"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v, %v", info, err)
	}
}

func TestSourcesSyncRefusesToOverwriteLocalEdits(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")
	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, project, ".ai/skills/alpha/SKILL.md", "alpha edited\n")

	_, err := app.Run(project, []string{"sources", "sync", project})
	if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "edited locally") {
		t.Fatalf("local edit was not reported: %v", err)
	}
	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "alpha edited\n" {
		t.Fatalf("local edit was overwritten: %q", got)
	}

	if _, err := app.Run(project, []string{"sources", "sync", project, "--force"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "alpha v1\n" {
		t.Fatalf("--force did not restore the pinned skill: %q", got)
	}
}

func TestSourcesSyncRefusesAnUnmanagedSkillWithTheSameName(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/skills/alpha/SKILL.md", "mine\n")
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")

	_, err := app.Run(project, []string{"sources", "sync", project})
	if err == nil || !strings.Contains(err.Error(), "not managed by sources") {
		t.Fatalf("an existing skill was not protected: %v", err)
	}
	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "mine\n" {
		t.Fatalf("existing skill was overwritten: %q", got)
	}
}

func TestSourcesSyncFailsWhenASkillIsMissingUpstream(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha\n"})
	writeSourcesYAML(t, project, repo, pinned, "ghost")

	_, err := app.Run(project, []string{"sources", "sync", project})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("missing skill was not reported: %v", err)
	}
}

func TestSourcesAddAppendsASkillAtTheSamePin(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha\n", "skills/beta/SKILL.md": "beta v1\n"})
	repo.commit(map[string]string{"skills/beta/SKILL.md": "beta v2\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")

	if _, err := app.Run(project, []string{"sources", "add", project, "pstack", "beta"}); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, project, ".ai/skills/beta/SKILL.md"); got != "beta v1\n" {
		t.Fatalf("added skill is not at the pinned ref: %q", got)
	}
	config := readFile(t, project, ".ai/sources.yaml")
	if !strings.Contains(config, pinned) || !strings.Contains(config, "alpha") || !strings.Contains(config, "beta") {
		t.Fatalf("sources.yaml lost the pin or a skill:\n%s", config)
	}
}

func TestSourcesAddCreatesTheSourceFromAGitURLAtItsHead(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n"})
	head := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v2\n"})

	if _, err := app.Run(project, []string{"sources", "add", project, "pstack", "alpha", "--git", repo.dir}); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "alpha v2\n" {
		t.Fatalf("new source is not at HEAD: %q", got)
	}
	if config := readFile(t, project, ".ai/sources.yaml"); !strings.Contains(config, head) || !strings.Contains(config, repo.dir) {
		t.Fatalf("sources.yaml did not record the url and pin:\n%s", config)
	}
}

func TestSourcesAddWarnsWhenASkillLinksToASkillThatIsNotVendored(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{
		"skills/alpha/SKILL.md": "See [beta](../beta/SKILL.md).\n",
		"skills/beta/SKILL.md":  "beta\n",
	})
	writeFile(t, project, ".ai/sources.yaml", "sources:\n  pstack:\n    git: "+repo.dir+"\n    ref: "+pinned+"\n    skills: []\n")

	out, err := app.Run(project, []string{"sources", "add", project, "pstack", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "warning") || !strings.Contains(out, "beta") {
		t.Fatalf("missing sibling skill was not reported:\n%s", out)
	}
}

func TestSourcesAvailableListsTheSkillsUpstreamAtThePin(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "a\n", "skills/beta/SKILL.md": "b\n", "skills/notes.md": "not a skill\n"})
	repo.commit(map[string]string{"skills/gamma/SKILL.md": "g\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")

	out, err := app.Run(project, []string{"sources", "available", project, "pstack"})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || strings.Join(doc.Skills, ",") != "alpha,beta" {
		t.Fatalf("unexpected skills %v: %s, %v", doc.Skills, out, err)
	}
}

func TestSourcesUpdatesReportsOnlyChangesToTheListedSkills(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n", "skills/beta/SKILL.md": "beta v1\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")
	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}

	state := func() (string, []string) {
		t.Helper()
		out, err := app.Run(project, []string{"sources", "updates", project})
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Sources []struct {
				Name    string   `json:"name"`
				State   string   `json:"state"`
				Changed []string `json:"changed_skills"`
			} `json:"sources"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Sources) != 1 {
			t.Fatalf("unexpected output: %s, %v", out, err)
		}
		return doc.Sources[0].State, doc.Sources[0].Changed
	}

	if got, _ := state(); got != "current" {
		t.Fatalf("state at the pinned head = %q", got)
	}
	repo.commit(map[string]string{"skills/beta/SKILL.md": "beta v2\n"})
	if got, _ := state(); got != "unchanged" {
		t.Fatalf("upstream moved only an unlisted skill, state = %q", got)
	}
	repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v2\n"})
	if got, changed := state(); got != "update_available" || strings.Join(changed, ",") != "alpha" {
		t.Fatalf("state = %q, changed = %v", got, changed)
	}
}

func TestSourcesUpdateMovesThePinAndRefreshesTheSkills(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")
	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}
	head := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v2\n"})

	if _, err := app.Run(project, []string{"sources", "update", project, "pstack"}); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, project, ".ai/skills/alpha/SKILL.md"); got != "alpha v2\n" {
		t.Fatalf("skill was not refreshed: %q", got)
	}
	if config := readFile(t, project, ".ai/sources.yaml"); !strings.Contains(config, head) || strings.Contains(config, pinned) {
		t.Fatalf("pin was not moved:\n%s", config)
	}
	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatalf("lock is stale after update: %v", err)
	}
}

func TestSourcesUpdateKeepsThePinWhenTheSyncIsRefused(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	pinned := repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v1\n"})
	writeSourcesYAML(t, project, repo, pinned, "alpha")
	if _, err := app.Run(project, []string{"sources", "sync", project}); err != nil {
		t.Fatal(err)
	}
	repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha v2\n"})
	writeFile(t, project, ".ai/skills/alpha/SKILL.md", "alpha edited\n")

	if _, err := app.Run(project, []string{"sources", "update", project, "pstack"}); err == nil {
		t.Fatal("update overwrote a local edit")
	}
	if config := readFile(t, project, ".ai/sources.yaml"); !strings.Contains(config, pinned) {
		t.Fatalf("pin moved although the sync was refused:\n%s", config)
	}
}

func TestSourcesRejectsARefThatIsNotACommit(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	repo := newUpstreamRepo(t)
	repo.commit(map[string]string{"skills/alpha/SKILL.md": "alpha\n"})
	writeSourcesYAML(t, project, repo, "main", "alpha")

	if _, err := app.Run(project, []string{"sources", "sync", project}); err == nil || !strings.Contains(err.Error(), "40") {
		t.Fatalf("a branch name was accepted as a pin: %v", err)
	}
}
