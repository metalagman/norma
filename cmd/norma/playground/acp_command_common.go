package playgroundcmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newACPPlaygroundCommand(
	use string,
	short string,
	bindFlags func(*cobra.Command),
	runFunc func(context.Context, string, io.Reader, io.Writer, io.Writer) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:          use,
		Short:        short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			return runFunc(cmd.Context(), repoRoot, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	bindFlags(cmd)
	return cmd
}
