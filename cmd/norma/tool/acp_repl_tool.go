package toolcmd

import (
	toolapps "github.com/metalagman/norma/internal/apps/tools"
	"github.com/spf13/cobra"
)

var runACPREPL = toolapps.RunACPToolREPL

func acpReplToolCommand() *cobra.Command {
	return toolapps.NewACPReplCommand(proxyRuntimeConfig(), toolapps.ACPREPLDeps{
		RunREPL: runACPREPL,
	})
}
