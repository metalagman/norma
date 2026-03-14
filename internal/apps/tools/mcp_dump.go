package tools

import (
	"context"
	"io"

	"github.com/metalagman/norma/internal/inspect/mcpinspect"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

// MCPDumpRunFunc executes MCP inspection against a command.
type MCPDumpRunFunc = DumpRunFunc

// MCPDumpDeps customizes MCP dump command runtime dependencies.
type MCPDumpDeps = DumpDeps

// NewMCPDumpCommand creates the mcp-dump command.
func NewMCPDumpCommand(runtime RuntimeConfig, deps MCPDumpDeps) *cobra.Command {
	runInspector := resolveDumpRunFunc(deps, func(
		ctx context.Context,
		workingDir string,
		command []string,
		jsonOutput bool,
		logLevel zerolog.Level,
		stdout io.Writer,
		stderr io.Writer,
	) error {
		return mcpinspect.Run(ctx, mcpinspect.RunConfig{
			Command:      command,
			WorkingDir:   workingDir,
			Component:    "tool.mcp_dump",
			StartMessage: "inspecting MCP server from custom command",
			JSONOutput:   jsonOutput,
			LogLevel:     logLevel,
			Stdout:       stdout,
			Stderr:       stderr,
		})
	})
	return newDumpCommand(
		runtime,
		"mcp-dump [--json] -- <mcp-server-cmd> [args...]",
		"Inspect any stdio MCP server command",
		"Start a stdio MCP server command and print initialize/capability information.",
		"  norma tool mcp-dump -- codex mcp-server\n  norma tool mcp-dump --json -- codex mcp-server --sandbox workspace-write",
		requireMCPCommandAfterDash,
		runInspector,
	)
}
