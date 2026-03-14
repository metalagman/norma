package main

import (
	"os"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
)

func main() {
	if err := toolapps.NewACPDumpCommand(toolapps.StandaloneRuntimeConfig(), toolapps.ACPDumpDeps{}).Execute(); err != nil {
		os.Exit(1)
	}
}
