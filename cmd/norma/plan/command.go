package plancmd

import (
	"github.com/spf13/cobra"
)

// Command builds the `norma plan` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plan",
		Short:   "Plan subcommands: tui, repl, web",
		Long:    "Plan and decompose issues using AI agents. Use subcommands to launch the interactive planner:\n\n  $ norma plan tui   Launch the interactive TUI for planning\n  $ norma plan repl   Run the planner in a line-based REPL",
		Example: "  norma plan tui\n  norma plan repl\n  norma plan web",
	}

	cmd.AddCommand(tuiCommand())
	cmd.AddCommand(replCommand())
	cmd.AddCommand(webCommand())
	return cmd
}
