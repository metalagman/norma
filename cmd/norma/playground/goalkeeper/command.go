package goalkeepercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/internal/apps/goalkeeper"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type options struct {
	bridgeBin     string
	maxIterations uint
}

// Command builds the `norma playground goalkeeper` command.
func Command() *cobra.Command {
	opts := options{}
	cmd := &cobra.Command{
		Use:          "goalkeeper <goal>",
		Short:        "Run the Goalkeeper worker-then-validator playground",
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
			return goalkeeper.Run(cmd.Context(), goalkeeper.Config{
				Goal:          strings.Join(args, " "),
				WorkingDir:    workingDir,
				BridgeBin:     opts.bridgeBin,
				MaxIterations: opts.maxIterations,
				Stdout:        cmd.OutOrStdout(),
				Stderr:        cmd.ErrOrStderr(),
				Logger:        &log.Logger,
			})
		},
	}
	cmd.Flags().StringVar(&opts.bridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to npx @normahq/codex-acp-bridge@1.6.3)")
	cmd.Flags().UintVar(&opts.maxIterations, "max-iterations", 5, "maximum worker-validator retry iterations")
	return cmd
}
