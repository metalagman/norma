package toolcmd

import (
	"context"
	"io"
	"os"

	"github.com/metalagman/norma/internal/adk/acpinspect"
	"github.com/spf13/cobra"
)

var runACPDumpInspector = func(
	ctx context.Context,
	repoRoot string,
	command []string,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return acpinspect.Run(ctx, acpinspect.RunConfig{
		Command:      command,
		WorkingDir:   repoRoot,
		Component:    "tool.acp_dump",
		StartMessage: "inspecting ACP agent from custom command",
		JSONOutput:   jsonOutput,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}

func acpDumpToolCommand() *cobra.Command {
	jsonOutput := false
	cmd := &cobra.Command{
		Use:          "acp-dump [--json] -- <acp-server-cmd> [args...]",
		Short:        "Inspect any stdio ACP server command",
		Long:         "Start a stdio ACP server command and print ACP initialize/session information.",
		Example:      "  norma tool acp-dump -- opencode acp\n  norma tool acp-dump --json -- gemini --experimental-acp",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			acpCommand, err := requireACPCommandAfterDash(cmd, args)
			if err != nil {
				return err
			}

			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}
			return runACPDumpInspector(
				cmd.Context(),
				repoRoot,
				acpCommand,
				jsonOutput,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print output as JSON")
	return cmd
}
