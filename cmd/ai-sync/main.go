package main

import (
	"fmt"
	"os"

	"github.com/dialguiba/ai-sync/internal/app"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	out, err := app.RunWithBuildInfo(".", os.Args[1:], app.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
