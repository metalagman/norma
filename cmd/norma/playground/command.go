package playgroundcmd

import (
	goalkeepercmd "github.com/normahq/norma/cmd/norma/playground/goalkeeper"
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "playground",
		Short:        "Reserved command group for internal playground integrations",
		Long:         "Reserved command group for internal playground integrations.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(goalkeepercmd.Command())
	return cmd
}
