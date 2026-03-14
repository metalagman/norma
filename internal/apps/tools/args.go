package tools

import (
	"fmt"

	"github.com/spf13/cobra"
)

func requireACPCommandAfterDash(cmd *cobra.Command, args []string) ([]string, error) {
	dashIndex := cmd.ArgsLenAtDash()
	if dashIndex < 0 {
		return nil, fmt.Errorf("missing command delimiter --; pass ACP server command after --")
	}
	if dashIndex > 0 {
		return nil, fmt.Errorf("arguments before -- are not allowed; pass ACP server command only after --")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("acp server command is required after --")
	}
	return append([]string(nil), args...), nil
}

func requireMCPCommandAfterDash(cmd *cobra.Command, args []string) ([]string, error) {
	dashIndex := cmd.ArgsLenAtDash()
	if dashIndex < 0 {
		return nil, fmt.Errorf("missing command delimiter --; pass MCP server command after --")
	}
	if dashIndex > 0 {
		return nil, fmt.Errorf("arguments before -- are not allowed; pass MCP server command only after --")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("mcp server command is required after --")
	}
	return append([]string(nil), args...), nil
}
