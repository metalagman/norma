package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	codexacp "github.com/metalagman/norma/internal/codex/acp"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

// CodexACPBridgeRunFunc executes the Codex ACP bridge runtime.
type CodexACPBridgeRunFunc func(
	ctx context.Context,
	workingDir string,
	opts codexacp.Options,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error

// CodexACPBridgeDeps customizes Codex ACP bridge command runtime dependencies.
type CodexACPBridgeDeps struct {
	RunProxy CodexACPBridgeRunFunc
}

func defaultCodexACPBridgeRun(
	ctx context.Context,
	workingDir string,
	opts codexacp.Options,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return codexacp.RunProxy(ctx, workingDir, opts, stdin, stdout, stderr)
}

// NewCodexACPBridgeCommand creates the codex-acp-bridge command.
func NewCodexACPBridgeCommand(runtime RuntimeConfig, deps CodexACPBridgeDeps) *cobra.Command {
	runProxy := deps.RunProxy
	if runProxy == nil {
		runProxy = defaultCodexACPBridgeRun
	}

	opts := codexacp.Options{Name: codexacp.DefaultAgentName}
	var codexConfigJSON string
	debugLogs := false

	cmd := &cobra.Command{
		Use:          "codex-acp-bridge [flags]",
		Short:        "Expose Codex MCP server as ACP over stdio",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			runOpts := opts
			if strings.TrimSpace(codexConfigJSON) != "" {
				var config map[string]any
				if err := json.Unmarshal([]byte(codexConfigJSON), &config); err != nil {
					return fmt.Errorf("parse --codex-config JSON object: %w", err)
				}
				runOpts.CodexConfig = config
			}
			level := runtime.resolveLogLevel(zerolog.ErrorLevel, debugLogs)
			runOpts.LogLevel = &level
			return runProxy(cmd.Context(), workingDir, runOpts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "ACP agent name exposed via initialize")
	cmd.Flags().StringVar(&opts.CodexModel, "codex-model", "", "Codex MCP `codex` tool model argument")
	cmd.Flags().StringVar(&opts.CodexSandbox, "codex-sandbox", "", "Codex MCP `codex` tool sandbox mode (read-only|workspace-write|danger-full-access)")
	cmd.Flags().StringVar(&opts.CodexApprovalPolicy, "codex-approval-policy", "", "Codex MCP `codex` tool approval policy (untrusted|on-failure|on-request|never)")
	cmd.Flags().StringVar(&opts.CodexProfile, "codex-profile", "", "Codex MCP `codex` tool profile argument")
	cmd.Flags().StringVar(&opts.CodexBaseInstructions, "codex-base-instructions", "", "Codex MCP `codex` tool base-instructions argument")
	cmd.Flags().StringVar(&opts.CodexDeveloperInstructions, "codex-developer-instructions", "", "Codex MCP `codex` tool developer-instructions argument")
	cmd.Flags().StringVar(&opts.CodexCompactPrompt, "codex-compact-prompt", "", "Codex MCP `codex` tool compact-prompt argument")
	cmd.Flags().StringVar(&codexConfigJSON, "codex-config", "", "Codex MCP `codex` tool config JSON object")
	if runtime.IncludeDebugFlag {
		cmd.Flags().BoolVar(&debugLogs, "debug", false, "enable debug logging")
	}
	cmd.Long = "Run a local Codex MCP server and expose it as an ACP agent over stdio. Use --codex-* flags to configure the Codex MCP `codex` tool call."
	cmd.Example = "  norma tool codex-acp-bridge\n  norma tool codex-acp-bridge --codex-model gpt-5.4 --codex-sandbox workspace-write\n  norma tool codex-acp-bridge --name team-codex\n  norma tool codex-acp-bridge --codex-approval-policy on-request --codex-config '{\"env\":\"dev\"}'"
	return cmd
}
