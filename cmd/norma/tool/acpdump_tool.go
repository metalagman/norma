package toolcmd

import (
	"context"
	"fmt"
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
		Component:    "tool.acpdump",
		StartMessage: "inspecting ACP agent from custom command",
		JSONOutput:   jsonOutput,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}

func acpDumpToolCommand() *cobra.Command {
	jsonOutput := false
	cmd := &cobra.Command{
		Use:          "acpdump [--json] -- <acp-server-cmd> [args...]",
		Short:        "Inspect any stdio ACP server command",
		Long:         "Start a stdio ACP server command and print ACP initialize/session information.",
		Example:      "  norma tool acpdump -- opencode acp\n  norma tool acpdump --json -- gemini --experimental-acp",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dashIndex := cmd.ArgsLenAtDash()
			if dashIndex < 0 {
				return fmt.Errorf("missing command delimiter --; pass ACP server command after --")
			}
			if dashIndex > 0 {
				return fmt.Errorf("arguments before -- are not allowed; pass ACP server command only after --")
			}
			if len(args) == 0 {
				return fmt.Errorf("acp server command is required after --")
			}

			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			return runACPDumpInspector(
				cmd.Context(),
				repoRoot,
				append([]string(nil), args...),
				jsonOutput,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print output as JSON")
	return cmd
}
