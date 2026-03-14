package tools

import (
	"context"
	"io"

	"github.com/metalagman/norma/internal/inspect/acpinspect"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

// ACPDumpRunFunc executes ACP inspection against a command.
type ACPDumpRunFunc = DumpRunFunc

// ACPDumpDeps customizes ACP dump command runtime dependencies.
type ACPDumpDeps = DumpDeps

// NewACPDumpCommand creates the acp-dump command.
func NewACPDumpCommand(runtime RuntimeConfig, deps ACPDumpDeps) *cobra.Command {
	runInspector := resolveDumpRunFunc(deps, func(
		ctx context.Context,
		workingDir string,
		command []string,
		jsonOutput bool,
		logLevel zerolog.Level,
		stdout io.Writer,
		stderr io.Writer,
	) error {
		return acpinspect.Run(ctx, acpinspect.RunConfig{
			Command:      command,
			WorkingDir:   workingDir,
			Component:    "tool.acp_dump",
			StartMessage: "inspecting ACP agent from custom command",
			JSONOutput:   jsonOutput,
			LogLevel:     logLevel,
			Stdout:       stdout,
			Stderr:       stderr,
		})
	})
	return newDumpCommand(
		runtime,
		"acp-dump [--json] -- <acp-server-cmd> [args...]",
		"Inspect any stdio ACP server command",
		"Start a stdio ACP server command and print ACP initialize/session information.",
		"  norma tool acp-dump -- opencode acp\n  norma tool acp-dump --json -- gemini --experimental-acp",
		requireACPCommandAfterDash,
		runInspector,
	)
}
