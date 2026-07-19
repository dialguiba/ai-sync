package main

import (
	"fmt"
	"os"

	"github.com/gentle-ai/ai-sync/internal/app"
)

func main() {
	out, err := app.Run(".", os.Args[1:])
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
