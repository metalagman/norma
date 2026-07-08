package taskmasterchatcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/normahq/norma/v2/internal/apps/taskmasterchat"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type options struct {
	bridgeBin string
}

// Command builds the `norma taskmaster-chat` command.
func Command() *cobra.Command {
	opts := options{}
	cmd := &cobra.Command{
		Use:          "taskmaster-chat",
		Short:        "Run the experimental generic Taskmaster harness as a local fake chat",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current working directory: %w", err)
			}
			workingDir, err = filepath.Abs(workingDir)
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			return taskmasterchat.Run(cmd.Context(), taskmasterchat.Config{
				WorkingDir: workingDir,
				BridgeBin:  opts.bridgeBin,
				Stdin:      cmd.InOrStdin(),
				Stdout:     cmd.OutOrStdout(),
				Stderr:     cmd.ErrOrStderr(),
				Logger:     &log.Logger,
			})
		},
	}
	cmd.Flags().StringVar(&opts.bridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to npx @normahq/codex-acp-bridge@1.6.3)")
	return cmd
}
