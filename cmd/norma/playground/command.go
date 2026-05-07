package playgroundcmd

import (
	pdcasynccmd "github.com/normahq/norma/cmd/norma/playground/pdcasync"
	pdcataskmastercmd "github.com/normahq/norma/cmd/norma/playground/pdcataskmaster"
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
	cmd.AddCommand(pdcasynccmd.Command())
	cmd.AddCommand(pdcataskmastercmd.Command())
	cmd.AddCommand(taskmastercmd.Command())
	return cmd
}
