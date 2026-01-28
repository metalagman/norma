package main

import (
	"fmt"

	"github.com/metalagman/norma/internal/run"
	"github.com/spf13/cobra"
)

func purgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Purge all runs, their directories, and associated git worktrees",
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, repoRoot, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()

			if err := run.Purge(cmd.Context(), storeDB, repoRoot); err != nil {
				return fmt.Errorf("purge failed: %w", err)
			}

			fmt.Println("Successfully purged all runs and worktrees.")
			return nil
		},
	}
}
