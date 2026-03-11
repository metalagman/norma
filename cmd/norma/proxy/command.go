package proxycmd

import (
	"github.com/spf13/cobra"
)

// Command builds the `norma proxy` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "proxy",
		Short:        "Proxy commands for protocol bridges",
		Long:         "Run protocol bridge helpers that expose one agent protocol through another.",
		Example:      "  norma proxy codex-acp --name team-codex",
		SilenceUsage: true,
	}
	cmd.AddCommand(codexACPProxyCommand())
	return cmd
}
