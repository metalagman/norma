package goalkeepercmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/internal/apps/goalkeeper"
	"github.com/spf13/cobra"
)

type options struct {
	bridgeBin    string
	maxToolCalls int
}

// Command builds the `norma playground goalkeeper` command.
func Command() *cobra.Command {
	opts := options{maxToolCalls: 8}
	cmd := &cobra.Command{
		Use:          "goalkeeper <goal>",
		Short:        "Run the experimental Goalkeeper PDCA scheduler playground",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.maxToolCalls < 0 {
				return fmt.Errorf("max-tool-calls must be >= 0")
			}
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current working directory: %w", err)
			}
			workingDir, err = filepath.Abs(workingDir)
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			return goalkeeper.Run(cmd.Context(), goalkeeper.Config{
				Goal:         strings.Join(args, " "),
				WorkingDir:   workingDir,
				BridgeBin:    opts.bridgeBin,
				MaxToolCalls: opts.maxToolCalls,
				Stdout:       cmd.OutOrStdout(),
				Stderr:       cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&opts.bridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to npx @normahq/codex-acp-bridge@latest)")
	cmd.Flags().IntVar(&opts.maxToolCalls, "max-tool-calls", opts.maxToolCalls, "maximum scheduler calls to goalkeeper.run_job")
	return cmd
}
