package prunecmd

import (
	"fmt"

	"github.com/normahq/norma/v2/internal/git"
	"github.com/normahq/norma/v2/internal/run"
	"github.com/spf13/cobra"
)

// Command builds the top-level `norma prune` command.
func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Prune all runs, their directories, associated worktrees, and stale norma task branches",
		RunE: func(cmd *cobra.Command, _ []string) error {
			storeDB, workingDir, closeFn, err := openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer closeFn()

			if !git.Available(cmd.Context(), workingDir) {
				return fmt.Errorf("current directory is not a git repository")
			}

			if err := run.Prune(cmd.Context(), storeDB, workingDir); err != nil {
				return fmt.Errorf("prune failed: %w", err)
			}

			fmt.Println("Successfully pruned all runs, worktrees, and stale norma task branches.")
			return nil
		},
	}
}
