package main

import (
	"os"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
)

func main() {
	if err := toolapps.NewMCPDumpCommand(toolapps.StandaloneRuntimeConfig(), toolapps.MCPDumpDeps{}).Execute(); err != nil {
		os.Exit(1)
	}
}
