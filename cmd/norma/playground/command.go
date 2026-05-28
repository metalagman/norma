package playgroundcmd

import (
	goalkeepercmd "github.com/normahq/norma/cmd/norma/playground/goalkeeper"
	goalkeeperactorcmd "github.com/normahq/norma/cmd/norma/playground/goalkeeperactor"
	pdcasynccmd "github.com/normahq/norma/cmd/norma/playground/pdcasync"
	pdcataskmastercmd "github.com/normahq/norma/cmd/norma/playground/pdcataskmaster"
	taskmastercmd "github.com/normahq/norma/cmd/norma/playground/taskmaster"
	taskmasterchatcmd "github.com/normahq/norma/cmd/norma/playground/taskmasterchat"
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
	cmd.AddCommand(goalkeeperactorcmd.Command())
	cmd.AddCommand(pdcasynccmd.Command())
	cmd.AddCommand(pdcataskmastercmd.Command())
	cmd.AddCommand(taskmastercmd.Command())
	cmd.AddCommand(taskmasterchatcmd.Command())
	return cmd
}
