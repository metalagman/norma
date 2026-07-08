package pdcasynccmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/v2/internal/apps/pdcasync"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

type options struct {
	bridgeBin     string
	maxIterations int
}

// Command builds the `norma pdca-sync` command.
func Command() *cobra.Command {
	opts := options{}
	cmd := &cobra.Command{
		Use:          "pdca-sync <goal>",
		Short:        "Run the experimental synchronous PDCA coordinator app",
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
			return pdcasync.Run(cmd.Context(), pdcasync.Config{
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
	cmd.Flags().IntVar(&opts.maxIterations, "max-iterations", 5, "maximum number of act->plan loopback iterations")
	return cmd
}
