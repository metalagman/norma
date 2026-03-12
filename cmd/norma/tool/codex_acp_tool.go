package toolcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	codexacp "github.com/metalagman/norma/internal/codex/acp"
	"github.com/spf13/cobra"
)

func codexACPToolCommand() *cobra.Command {
	opts := codexacp.Options{Name: codexacp.DefaultAgentName}
	var codexConfigJSON string
	cmd := &cobra.Command{
		Use:          "codex-acp [flags]",
		Short:        "Expose Codex MCP server as ACP over stdio",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := os.Getwd()
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
			return codexacp.RunProxy(cmd.Context(), repoRoot, runOpts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
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
	cmd.Long = "Run a local Codex MCP server and expose it as an ACP agent over stdio. Use --codex-* flags to configure the Codex MCP `codex` tool call."
	cmd.Example = "  norma tool codex-acp\n  norma tool codex-acp --codex-model gpt-5.4 --codex-sandbox workspace-write\n  norma tool codex-acp --name team-codex\n  norma tool codex-acp --codex-approval-policy on-request --codex-config '{\"env\":\"dev\"}'"
	return cmd
}
