package main

import (
	"testing"

	"github.com/dialguiba/ai-sync/internal/app"
)

func TestResolveBuildInfoUsesModuleVersionWhenLdflagsAreDefault(t *testing.T) {
	got := resolveBuildInfo(
		app.BuildInfo{Version: "dev", Commit: "unknown", Date: "unknown"},
		"v1.2.3",
		map[string]string{},
	)

	if got.Version != "v1.2.3" {
		t.Fatalf("expected module version fallback, got %q", got.Version)
	}
}

func TestResolveBuildInfoKeepsLdflagsVersion(t *testing.T) {
	got := resolveBuildInfo(
		app.BuildInfo{Version: "v1.2.3", Commit: "abc123", Date: "2026-07-19T00:00:00Z"},
		"v9.9.9",
		map[string]string{"vcs.revision": "ignored", "vcs.time": "ignored"},
	)

	want := app.BuildInfo{Version: "v1.2.3", Commit: "abc123", Date: "2026-07-19T00:00:00Z"}
	if got != want {
		t.Fatalf("expected ldflags metadata to win, got %+v", got)
	}
}

func TestResolveBuildInfoUsesVCSSettingsWhenAvailable(t *testing.T) {
	got := resolveBuildInfo(
		app.BuildInfo{Version: "dev", Commit: "unknown", Date: "unknown"},
		"(devel)",
		map[string]string{"vcs.revision": "abc123", "vcs.time": "2026-07-19T00:00:00Z"},
	)

	if got.Commit != "abc123" || got.Date != "2026-07-19T00:00:00Z" {
		t.Fatalf("expected VCS settings fallback, got %+v", got)
	}
}
