package taskmastercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/internal/apps/taskmaster"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type options struct {
	bridgeBin string
}

// Command builds the `norma playground taskmaster` command.
func Command() *cobra.Command {
	opts := options{}
	cmd := &cobra.Command{
		Use:          "taskmaster <content>",
		Short:        "Run the experimental generic Taskmaster async harness",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current working directory: %w", err)
			}
			workingDir, err = filepath.Abs(workingDir)
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			return taskmaster.Run(cmd.Context(), taskmaster.Config{
				Content:    strings.Join(args, " "),
				WorkingDir: workingDir,
				BridgeBin:  opts.bridgeBin,
				Stdout:     cmd.OutOrStdout(),
				Stderr:     cmd.ErrOrStderr(),
				Logger:     &log.Logger,
			})
		},
	}
	cmd.Flags().StringVar(&opts.bridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to npx @normahq/codex-acp-bridge@latest)")
	return cmd
}
