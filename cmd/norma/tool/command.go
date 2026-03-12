package toolcmd

import (
	"github.com/spf13/cobra"
)

// Command builds the `norma tool` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tool",
		Short:        "Tool commands for protocol bridges",
		Long:         "Run tool helpers that expose one agent protocol through another.",
		Example:      "  norma tool codex-acp --name team-codex\n  norma tool acpdump -- opencode acp",
		SilenceUsage: true,
	}
	cmd.AddCommand(acpDumpToolCommand())
	cmd.AddCommand(codexACPToolCommand())
	return cmd
}
