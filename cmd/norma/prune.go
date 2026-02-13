package main

import (
	"fmt"

	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/run"
	"github.com/spf13/cobra"
)

func pruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Prune all runs, their directories, associated worktrees, and stale norma task branches",
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, repoRoot, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()

			if !git.Available(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			if err := run.Prune(cmd.Context(), storeDB, repoRoot); err != nil {
				return fmt.Errorf("prune failed: %w", err)
			}

			fmt.Println("Successfully pruned all runs, worktrees, and stale norma task branches.")
			return nil
		},
	}
}
