package mcpcmd

import "github.com/spf13/cobra"

// Command builds the `norma mcp` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mcp",
		Short:        "MCP servers exposed by norma",
		SilenceUsage: true,
	}
	cmd.AddCommand(TasksCommand())
	return cmd
}
