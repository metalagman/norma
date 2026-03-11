package playgroundcmd

import (
	acpcmd "github.com/metalagman/norma/cmd/norma/playground/acp"
	"github.com/spf13/cobra"
)

// Command builds the `norma playground` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "playground",
		Short:        "Experimental playground commands for agent integrations",
		SilenceUsage: true,
	}
	cmd.AddCommand(acpcmd.Command())
	cmd.AddCommand(structuredCommand())
	cmd.AddCommand(codexMCPServerCommand())
	return cmd
}
