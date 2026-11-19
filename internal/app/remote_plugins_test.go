package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func writeNestedPlugin(t *testing.T, project string) {
	t.Helper()
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  local:\n    path: .ai/market\nplugins:\n  demo@local: true\n")
	writeFile(t, project, ".ai/market/.claude-plugin/marketplace.json", `{"name":"local","plugins":[{"name":"demo","source":"./demo"}]}`)
	writeFile(t, project, ".ai/market/demo/.claude-plugin/plugin.json", `{"name":"demo","version":"1.0.0","skills":["./skills/engineering/tdd"],"hooks":{"PreToolUse":[]}}`)
	writeFile(t, project, ".ai/market/demo/skills/engineering/tdd/SKILL.md", "---\nname: tdd\ndescription: Tests\n---\nUse demo:helper.\n")
	writeFile(t, project, ".ai/market/demo/skills/experimental/hidden/SKILL.md", "---\nname: hidden\ndescription: Hidden\n---\nHidden\n")
	writeFile(t, project, ".ai/market/demo/agents/helper.md", "---\nname: helper\n---\nReview tests\n")
	writeFile(t, project, ".ai/market/demo/.mcp.json", `{"mcpServers":{"docs":{"type":"http","url":"https://example.com/mcp"},"tool":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server.js"]}}}`)
	writeFile(t, project, ".ai/market/demo/server.js", "// fixture\n")
}

func TestPluginManifestSelectsNestedSkillsAndPropagatesMCP(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeNestedPlugin(t, project)
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 0")
	out, err := app.Run(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{".agents/skills", ".kiro/skills"} {
		assertFileContainsInOrder(t, filepath.Join(project, base, "demo-tdd/SKILL.md"), []string{"name: demo-tdd", "demo-helper"})
		assertFileContainsInOrder(t, filepath.Join(project, base, "demo-tdd/references/helper.md"), []string{"Review tests"})
		if _, err := os.Stat(filepath.Join(project, base, "demo-experimental")); !os.IsNotExist(err) {
			t.Fatal("copied undeclared skills")
		}
	}
	for _, f := range []string{".codex/config.toml", ".kiro/settings/mcp.json"} {
		assertFileContainsInOrder(t, filepath.Join(project, f), []string{"demo-docs", "https://example.com/mcp", "demo-tool"})
		assertFileNotContains(t, filepath.Join(project, f), "CLAUDE_PLUGIN_ROOT")
	}
	if !strings.Contains(out, "hooks") {
		t.Fatalf("unsupported hooks omitted silently: %s", out)
	}
}

func remoteMarketplaceFixture(t *testing.T) (string, *upstreamRepo, string) {
	t.Helper()
	repo := newUpstreamRepo(t)
	ref := repo.commit(map[string]string{
		".claude-plugin/marketplace.json": `{"name":"tools","plugins":[{"name":"demo","source":"./"}]}`,
		".claude-plugin/plugin.json":      `{"name":"demo","version":"1.0.0","description":"Demo","author":{"name":"Team"},"keywords":["demo"],"skills":["./skills/engineering/tdd"]}`,
		"skills/engineering/tdd/SKILL.md": "---\nname: tdd\ndescription: Tests\n---\nPinned content\n",
		".mcp.json":                       `{"mcpServers":{"docs":{"url":"https://example.com/mcp"}}}`,
	})
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: fixture/tools\nplugins:\n  demo@tools: true\n")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[url \""+repo.dir+"\"]\n insteadOf = https://github.com/fixture/tools.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	fakeClaudeOnPath(t, "exit 0")
	return project, repo, ref
}

func TestRemotePluginSyncPinsPropagatesAndPreviewsWithoutGlobalInstall(t *testing.T) {
	project, repo, ref := remoteMarketplaceFixture(t)
	out, err := app.Run(project, []string{"plugins", "sync", project})
	if err != nil {
		t.Fatalf("resolve: %v %s", err, out)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".ai/plugins.lock.yaml"), []string{ref})
	repo.commit(map[string]string{"skills/engineering/tdd/SKILL.md": "---\nname: tdd\ndescription: Tests\n---\nNew upstream content\n"})
	// Moving the upstream away proves subsequent generation and resolution use the pin offline.
	if err := os.Rename(repo.dir, repo.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if out, err = app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatalf("offline sync: %v %s", err, out)
	}
	if out, err = app.Run(project, nil); err != nil {
		t.Fatalf("generate: %v %s", err, out)
	}
	for _, base := range []string{".agents/skills", ".kiro/skills"} {
		assertFileContainsInOrder(t, filepath.Join(project, base, "demo-tdd/SKILL.md"), []string{"Pinned content"})
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".codex/config.toml"), []string{"demo-docs"})
	for _, target := range []string{"codex", "kiro"} {
		doc := getPreview(t, project, target)
		if len(doc.Plugins) != 1 || doc.Plugins[0].State != "compatible" || len(doc.Plugins[0].Skills) != 1 || doc.Plugins[0].Skills[0] != "tdd" {
			t.Fatalf("%s preview: %+v", target, doc)
		}
	}
	raw, err := app.Run(project, []string{"inventory", project})
	if err != nil {
		t.Fatal(err)
	}
	var inventory map[string]any
	if err = json.Unmarshal([]byte(raw), &inventory); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"type":"skill","name":"demo-tdd"`) {
		t.Fatal("plugin skill became standalone canonical resource")
	}
}

func TestRemotePluginDryRunDoesNotFetchOrWrite(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	if err := os.Rename(repo.dir, repo.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"plugins", "sync", project, "--dry-run"}, {"--dry-run"}} {
		out, err := app.Run(project, args)
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		if !strings.Contains(out, "plugins sync") && !strings.Contains(out, "would resolve") {
			t.Fatalf("missing pending resolution: %s", out)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".ai/plugins.lock.yaml")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote lock")
	}
}

func TestRemotePluginUpdateMovesPinOnlyWhenRequested(t *testing.T) {
	project, repo, ref := remoteMarketplaceFixture(t)
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	next := repo.commit(map[string]string{"skills/engineering/tdd/SKILL.md": "---\nname: tdd\ndescription: Tests\n---\nUpdated content\n"})
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".ai/plugins.lock.yaml"), []string{ref})
	if _, err := app.Run(project, []string{"plugins", "sync", project, "--update"}); err != nil {
		t.Fatal(err)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".ai/plugins.lock.yaml"), []string{next})
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatal(err)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".agents/skills/demo-tdd/SKILL.md"), []string{"Updated content"})
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: fixture/tools\nplugins:\n  demo@tools: false\n")
	if err := os.RemoveAll(filepath.Join(project, ".ai/plugins")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents/skills/demo-tdd/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("disabled plugin was not pruned")
	}
	assertFileNotContains(t, filepath.Join(project, ".codex/config.toml"), "demo-docs")
	if out, err := app.Run(project, []string{"inventory", project}); err != nil {
		t.Fatalf("disabled missing cache invalidated inventory: %v %s", err, out)
	}
}

func TestRemotePluginRejectsModifiedPackage(t *testing.T) {
	project, _, _ := remoteMarketplaceFixture(t)
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	var changed bool
	filepath.WalkDir(filepath.Join(project, ".ai/plugins"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !changed && entry.Name() == "SKILL.md" {
			os.WriteFile(path, []byte("tampered"), 0644)
			changed = true
		}
		return nil
	})
	if !changed {
		t.Fatal("no package was downloaded")
	}
	for _, args := range [][]string{{"--dry-run"}, {"plugins", "sync", project}, {"plugins", "preview", project, "--target", "codex"}} {
		out, err := app.Run(project, args)
		if err == nil && !strings.Contains(out, "modified") {
			t.Fatalf("accepted edited lock package %v: %s", args, out)
		}
	}
}

func TestPluginManifestRejectsEscapingAndSymlinkedComponents(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			project := t.TempDir()
			writeCanonicalSource(t, project)
			writeNestedPlugin(t, project)
			if kind == "traversal" {
				writeFile(t, project, ".ai/market/demo/.claude-plugin/plugin.json", `{"skills":["../../../outside"]}`)
			} else {
				outside := filepath.Join(t.TempDir(), "outside.json")
				os.WriteFile(outside, []byte(`{"mcpServers":{"x":{"command":"sh"}}}`), 0644)
				path := filepath.Join(project, ".ai/market/demo/.mcp.json")
				os.Remove(path)
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := app.Run(project, []string{"--dry-run"}); err == nil {
				t.Fatal("accepted escaping plugin component")
			}
		})
	}
}

func TestRemotePluginResolvesDeclaredGitVariant(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	ref := repo.commit(map[string]string{
		"native/plugin.json":           `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"kit","version":"1.0.0","description":"Kit","author":{"name":"Team"},"keywords":["kit"]}`,
		"native/skills/hello/SKILL.md": "---\nname: hello\ndescription: Hello\n---\nHello\n",
	})
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  kit:\n    enabled: true\n    targets:\n      codex:\n        git: https://github.com/fixture/tools.git\n        ref: "+ref+"\n        subdir: native\n      kiro:\n        git: https://github.com/fixture/tools.git\n        ref: "+ref+"\n        subdir: native\n")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"codex", "kiro"} {
		doc := getPreview(t, project, target)
		if len(doc.Plugins) != 1 || doc.Plugins[0].State != "compatible" {
			t.Fatalf("%s: %+v", target, doc)
		}
	}
}

func TestSyncCodexDoesNotInstallClaudePlugins(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeFile(t, project, ".ai/plugins.yaml", "plugins:\n  demo@tools: true\n")
	t.Setenv("HOME", t.TempDir())
	fakeClaudeOnPath(t, "exit 98")
	if out, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatalf("Codex sync invoked Claude: %v %s", err, out)
	}
}

func TestRemotePluginCanEnableAnotherMemberAtTheSamePinOffline(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	repo.commit(map[string]string{".claude-plugin/marketplace.json": `{"name":"tools","plugins":[{"name":"demo","source":"./"},{"name":"extra","source":"./"}]}`})
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: fixture/tools\nplugins:\n  demo@tools: true\n  extra@tools: true\n")
	if err := os.Rename(repo.dir, repo.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	doc := getPreview(t, project, "codex")
	if len(doc.Plugins) != 2 {
		t.Fatalf("second member was not prepared: %+v", doc)
	}
	for _, plugin := range doc.Plugins {
		if plugin.State != "compatible" {
			t.Fatalf("unexpected package state: %+v", plugin)
		}
	}
}

func TestRemotePluginPartialPackageStaysPendingInNativeApply(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	repo.commit(map[string]string{"hooks/hooks.json": `{"hooks":{"PreToolUse":[]}}`})
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	doc := getPreview(t, project, "kiro")
	if len(doc.Plugins) != 1 || doc.Plugins[0].State != "partial" || doc.Plugins[0].Action.Kind != "pending" || !strings.Contains(strings.Join(doc.Plugins[0].Omitted, ","), "hooks") {
		t.Fatalf("partial components hidden: %+v", doc)
	}
	out, err := app.Run(project, []string{"plugins", "apply", project, "--target", "kiro", "--fingerprint", doc.Fingerprint, "--all"})
	if err != nil || !strings.Contains(out, `"status":"pending"`) {
		t.Fatalf("partial package installed: %s %v", out, err)
	}
}

func TestPluginMCPManifestPathIsNotReadTwice(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeNestedPlugin(t, project)
	writeFile(t, project, ".ai/market/demo/.claude-plugin/plugin.json", `{"name":"demo","skills":["./skills/engineering/tdd"],"mcpServers":"./.mcp.json"}`)
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatalf("valid manifest references its default MCP file: %v", err)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".codex/config.toml"), []string{"demo-docs"})
}

func TestPluginProjectSkillsExpandPluginRootToPinnedPackage(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeNestedPlugin(t, project)
	writeFile(t, project, ".ai/market/demo/skills/engineering/tdd/SKILL.md", "---\nname: tdd\ndescription: Tests\n---\nRun ${CLAUDE_PLUGIN_ROOT}/server.js\n")
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatal(err)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".agents/skills/demo-tdd/SKILL.md"), []string{filepath.Join(project, ".ai/market/demo/server.js")})
}

func TestRemotePluginStatusRetainsClaudeIdentityAfterResolution(t *testing.T) {
	project, _, _ := remoteMarketplaceFixture(t)
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	out, err := app.Run(project, []string{"plugins", "status", project})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"claude_identity":"demo@tools"`) || !strings.Contains(out, `"declared_targets":["claude","codex","kiro"]`) {
		t.Fatalf("lost Claude identity after resolution: %s", out)
	}
}

func TestRemotePluginDereferencesInternalFileSymlinks(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	writeFile(t, repo.dir, "CLAUDE.md", "# Package instructions\n")
	if err := os.Symlink("CLAUDE.md", filepath.Join(repo.dir, "AGENTS.md")); err != nil {
		t.Skip(err)
	}
	repo.git("add", "-A")
	repo.git("commit", "-q", "-m", "internal link")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatalf("safe source symlink: %v", err)
	}
	doc := getPreview(t, project, "codex")
	if len(doc.Plugins) != 1 || doc.Plugins[0].State != "compatible" {
		t.Fatalf("not inspectable: %+v", doc)
	}
}

func TestPluginUnsupportedMCPDoesNotBlockClaudeOrSupportedServers(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeNestedPlugin(t, project)
	writeFile(t, project, ".ai/market/demo/.mcp.json", `{"mcpServers":{"events":{"type":"sse","url":"https://example.com/events"},"docs":{"url":"https://example.com/mcp"}}}`)
	if _, err := app.Run(project, []string{"--target", "claude", "--dry-run"}); err != nil {
		t.Fatalf("valid Claude package blocked by portable adaptation: %v", err)
	}
	out, err := app.Run(project, []string{"--target", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sse") {
		t.Fatalf("unsupported transport hidden: %s", out)
	}
	assertFileContainsInOrder(t, filepath.Join(project, ".codex/config.toml"), []string{"demo-docs"})
	assertFileNotContains(t, filepath.Join(project, ".codex/config.toml"), "demo-events")
}

func TestRemotePluginRestoresSharedPinAfterEnabledMembersChange(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	repo.commit(map[string]string{".claude-plugin/marketplace.json": `{"name":"tools","plugins":[{"name":"demo","source":"./"},{"name":"extra","source":"./"}]}`})
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  first:\n    repo: fixture/tools\n  second:\n    repo: fixture/tools\nplugins:\n  demo@first: true\n  extra@second: true\n")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  first:\n    repo: fixture/tools\n  second:\n    repo: fixture/tools\nplugins:\n  demo@first: true\n  extra@second: false\n")
	if err := os.RemoveAll(filepath.Join(project, ".ai/plugins")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatalf("inconsistent aliases after restore: %v", err)
	}
}

func TestRemotePluginSyncDoesNotExecutePackageOrInstallers(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	marker := filepath.Join(t.TempDir(), "executed")
	payload, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"evil": map[string]any{"command": "/bin/sh", "args": []string{"-c", "touch '" + marker + "'"}}}})
	repo.commit(map[string]string{".mcp.json": string(payload), "hooks/hooks.json": `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"touch ` + marker + `"}]}]}}`, "setup.sh": "touch '" + marker + "'\n"})
	fakeClaudeOnPath(t, "touch '"+marker+"'; exit 97")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("resolution executed package content or installer")
	}
}

func TestRemotePluginFailedLaterResolutionKeepsEarlierLockConsistent(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	repo.commit(map[string]string{".claude-plugin/marketplace.json": `{"name":"tools","plugins":[{"name":"demo","source":"./"},{"name":"extra","source":"./"}]}`})
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  a:\n    repo: fixture/tools\nplugins:\n  demo@a: true\n")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	configPath := os.Getenv("GIT_CONFIG_GLOBAL")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n[url \""+filepath.Join(t.TempDir(), "missing")+"\"]\n insteadOf = https://github.com/fixture/missing.git\n")...)
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  a:\n    repo: fixture/tools\n  z:\n    repo: fixture/missing\nplugins:\n  demo@a: true\n  extra@a: true\n  missing@z: true\n")
	if _, err = app.Run(project, []string{"plugins", "sync", project}); err == nil {
		t.Fatal("expected unavailable later repository to fail")
	}
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  a:\n    repo: fixture/tools\nplugins:\n  demo@a: true\n  extra@a: true\n")
	if _, err = app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatalf("earlier successful preparation corrupted lock: %v", err)
	}
}

func TestPluginUnknownManifestComponentsAreReported(t *testing.T) {
	project := t.TempDir()
	writeCanonicalSource(t, project)
	writeNestedPlugin(t, project)
	writeFile(t, project, ".ai/market/demo/.claude-plugin/plugin.json", `{"name":"demo","skills":["./skills/engineering/tdd"],"extensions":{"example.vendor":{"feature":true}}}`)
	out, err := app.Run(project, []string{"--target", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "extensions") {
		t.Fatalf("unadapted manifest components were silently dropped: %s", out)
	}
}

func TestRemotePluginRetainsExecutablePackageCommands(t *testing.T) {
	project, repo, _ := remoteMarketplaceFixture(t)
	writeFile(t, repo.dir, "bin/run.sh", "#!/bin/sh\nexit 0\n")
	writeFile(t, repo.dir, "skills/engineering/tdd/scripts/check.sh", "#!/bin/sh\nexit 0\n")
	for _, file := range []string{"bin/run.sh", "skills/engineering/tdd/scripts/check.sh"} {
		if err := os.Chmod(filepath.Join(repo.dir, file), 0755); err != nil {
			t.Fatal(err)
		}
	}
	repo.commit(map[string]string{".mcp.json": `{"mcpServers":{"tool":{"command":"${CLAUDE_PLUGIN_ROOT}/bin/run.sh"}}}`})
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	doc := getPreview(t, project, "kiro")
	if len(doc.Plugins) != 1 || doc.Plugins[0].State != "compatible" {
		t.Fatalf("command adapter: %+v", doc)
	}
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(project, ".agents/skills/demo-tdd/scripts/check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Fatal("remote executable lost its mode")
	}
}

func TestRemotePluginHonorsExplicitClaudeOnlyTarget(t *testing.T) {
	project, _, _ := remoteMarketplaceFixture(t)
	writeFile(t, project, ".ai/plugins.yaml", "marketplaces:\n  tools:\n    repo: fixture/tools\nplugins:\n  only-claude:\n    enabled: true\n    targets:\n      claude:\n        install: demo@tools\n")
	if _, err := app.Run(project, []string{"plugins", "sync", project}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(project, []string{"--target", "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents/skills/demo-tdd/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("explicit Claude-only plugin was propagated to Codex")
	}
	assertFileNotContains(t, filepath.Join(project, ".codex/config.toml"), "demo-docs")
}
