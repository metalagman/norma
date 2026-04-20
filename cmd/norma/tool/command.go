package toolcmd

import (
	"github.com/spf13/cobra"

	acpdump "github.com/normahq/norma/cmd/acp-dump/cmd"
	acprepl "github.com/normahq/norma/cmd/acp-repl/cmd"
	mcpdump "github.com/normahq/norma/cmd/mcp-dump/cmd"
)

// Command builds the `norma tool` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tool",
		Short:        "Tool commands for protocol bridges",
		Long:         "Run tool helpers that expose one agent protocol through another.",
		Example:      "  norma tool acp-dump -- opencode acp\n  norma tool mcp-dump -- codex mcp-server\n  norma tool acp-repl --model openai/gpt-5.4 --mode coding -- opencode acp",
		SilenceUsage: true,
	}
	cmd.AddCommand(acpdump.Command())
	cmd.AddCommand(mcpdump.Command())
	cmd.AddCommand(acprepl.Command())
	return cmd
}
