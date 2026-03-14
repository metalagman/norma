package toolcmd

import (
	toolapps "github.com/metalagman/norma/internal/apps/tools"
	"github.com/spf13/cobra"
)

func codexACPToolCommand() *cobra.Command {
	return toolapps.NewCodexACPBridgeCommand(proxyRuntimeConfig(), toolapps.CodexACPBridgeDeps{})
}
