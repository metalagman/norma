package main

import (
	"os"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
)

func main() {
	if err := toolapps.NewACPReplCommand(toolapps.StandaloneRuntimeConfig(), toolapps.ACPREPLDeps{}).Execute(); err != nil {
		os.Exit(1)
	}
}
