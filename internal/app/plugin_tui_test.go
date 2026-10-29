package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginTUIListsPluginsAndReturnsFromDetail(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".ai"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "project.md"), []byte("# Project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "mcp.yaml"), []byte("servers: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "plugins.yaml"), []byte("plugins:\n  kit@tools: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	var output bytes.Buffer
	if err := runPluginTUI(project, strings.NewReader("1\nb\nq\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1. kit@tools") || !strings.Contains(output.String(), "Claude not installed") || !strings.Contains(output.String(), "u. Update Claude plugin") {
		t.Fatalf("plugin menu missing expected status and actions:\n%s", output.String())
	}
}

func TestPluginTUIAddUsesCanonicalDeclaration(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".ai"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "project.md"), []byte("# Project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "mcp.yaml"), []byte("servers: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ai", "plugins.yaml"), []byte("marketplaces:\n  tools:\n    repo: org/tools\nplugins: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output bytes.Buffer
	if err := runPluginTUI(project, strings.NewReader("a\nkit@tools\ny\nq\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(project, ".ai", "plugins.yaml"))
	if err != nil || !strings.Contains(string(config), "kit@tools: true") {
		t.Fatalf("plugin was not declared in .ai/plugins.yaml: %s, %v", config, err)
	}
}
