package command

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	codexacpappserver "github.com/normahq/norma/internal/apps/codexacpappserver"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	runProxy    = codexacpappserver.RunProxy
	initLogging = logging.Init
)

func Command() *cobra.Command {
	opts := codexacpappserver.Options{}
	var codexConfigJSON string
	var debugLogs bool

	cmd := &cobra.Command{
		Use:          "codex-acp-app-server [flags]",
		Short:        "Expose Codex app-server as ACP over stdio",
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

			logLevel := logging.LevelInfo
			if debugLogs {
				logLevel = logging.LevelDebug
			}
			if err := initLogging(logging.WithLevel(logLevel)); err != nil {
				return fmt.Errorf("initialize logging: %w", err)
			}
			ctx := log.Logger.With().Str("component", "codex.acp").Logger().WithContext(cmd.Context())

			return runProxy(ctx, workingDir, runOpts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "ACP agent name exposed via initialize (defaults to Codex app-server user agent)")
	cmd.Flags().StringVar(&opts.CodexModel, "codex-model", "", "Codex app-server model override")
	cmd.Flags().StringVar(&opts.CodexSandbox, "codex-sandbox", "", "Codex app-server sandbox mode (read-only|workspace-write|danger-full-access)")
	cmd.Flags().StringVar(&opts.CodexApprovalPolicy, "codex-approval-policy", "", "Codex app-server approval policy (untrusted|on-failure|on-request|never)")
	cmd.Flags().StringVar(&opts.CodexProfile, "codex-profile", "", "Codex config profile override")
	cmd.Flags().StringVar(&opts.CodexBaseInstructions, "codex-base-instructions", "", "Codex base instructions override")
	cmd.Flags().StringVar(&opts.CodexDeveloperInstructions, "codex-developer-instructions", "", "Codex developer instructions override")
	cmd.Flags().StringVar(&opts.CodexCompactPrompt, "codex-compact-prompt", "", "Codex config compact_prompt override")
	cmd.Flags().StringVar(&codexConfigJSON, "codex-config", "", "Codex app-server config JSON object")
	cmd.Flags().BoolVar(&debugLogs, "debug", false, "enable debug logging")
	cmd.Long = "Run Codex app-server and expose it as an ACP agent over stdio. Use --codex-* flags to configure thread/start defaults and config overrides."
	//nolint:dupword
	cmd.Example = `  codex-acp-app-server
  codex-acp-app-server --codex-model gpt-5.4 --codex-sandbox workspace-write
  codex-acp-app-server --name team-codex
  codex-acp-app-server --codex-approval-policy on-request --codex-config '{"env":"dev"}'`
	return cmd
}
