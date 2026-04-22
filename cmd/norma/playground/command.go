package playgroundcmd

import (
	"github.com/spf13/cobra"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "playground",
		Short:        "Reserved command group for internal playground integrations",
		Long:         "Reserved command group for internal playground integrations; no public subcommands are currently exposed.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	return cmd
}
