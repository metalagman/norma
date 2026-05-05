package goalkeepercmd

import (
	"github.com/normahq/norma/internal/apps/goalkeeper"
	"github.com/spf13/cobra"
)

// NotifyCommand builds the `norma playground goalkeeper-notify` command.
func NotifyCommand() *cobra.Command {
	return newGoalkeeperCommand(
		"goalkeeper-notify <goal>",
		"Run the experimental Goalkeeper async notification playground",
		"maximum scheduler calls to Goalkeeper MCP tools",
		goalkeeper.RunNotify,
	)
}
