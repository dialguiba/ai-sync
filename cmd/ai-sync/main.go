package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/dialguiba/ai-sync/internal/app"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	out, err := app.RunWithBuildInfo(".", os.Args[1:], runtimeBuildInfo())
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runtimeBuildInfo() app.BuildInfo {
	build := app.BuildInfo{Version: version, Commit: commit, Date: date}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return build
	}

	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return resolveBuildInfo(build, info.Main.Version, settings)
}

func resolveBuildInfo(build app.BuildInfo, moduleVersion string, settings map[string]string) app.BuildInfo {
	if build.Version == "dev" && moduleVersion != "" && moduleVersion != "(devel)" {
		build.Version = moduleVersion
	}
	if build.Commit == "unknown" {
		if revision := settings["vcs.revision"]; revision != "" {
			build.Commit = revision
		}
	}
	if build.Date == "unknown" {
		if vcsTime := settings["vcs.time"]; vcsTime != "" {
			build.Date = vcsTime
		}
	}
	return build
}
