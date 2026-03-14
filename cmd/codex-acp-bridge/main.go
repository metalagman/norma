package main

import (
	"os"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
)

func main() {
	if err := toolapps.NewCodexACPBridgeCommand(toolapps.StandaloneRuntimeConfig(), toolapps.CodexACPBridgeDeps{}).Execute(); err != nil {
		os.Exit(1)
	}
}
