package toolcmd

import (
	"context"
	"io"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
	"github.com/metalagman/norma/internal/inspect/mcpinspect"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var runMCPDumpInspector = func(
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
}

func mcpDumpToolCommand() *cobra.Command {
	return toolapps.NewMCPDumpCommand(proxyRuntimeConfig(), toolapps.MCPDumpDeps{
		RunInspector: runMCPDumpInspector,
	})
}
