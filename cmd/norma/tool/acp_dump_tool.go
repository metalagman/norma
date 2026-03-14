package toolcmd

import (
	"context"
	"io"

	toolapps "github.com/metalagman/norma/internal/apps/tools"
	"github.com/metalagman/norma/internal/inspect/acpinspect"
	"github.com/metalagman/norma/internal/logging"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var runACPDumpInspector = func(
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
}

func acpDumpToolCommand() *cobra.Command {
	return toolapps.NewACPDumpCommand(proxyRuntimeConfig(), toolapps.ACPDumpDeps{
		RunInspector: runACPDumpInspector,
	})
}

func proxyRuntimeConfig() toolapps.RuntimeConfig {
	return toolapps.RuntimeConfig{
		DebugEnabled: logging.DebugEnabled,
	}
}
