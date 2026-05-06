package playgroundcmd

import (
	taskmastercmd "github.com/normahq/norma/cmd/norma/playground/taskmaster"
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
	cmd.AddCommand(taskmastercmd.Command())
	return cmd
}
