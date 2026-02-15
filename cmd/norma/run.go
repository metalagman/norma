package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/pdca"
	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "run <task-id>",
		Short:        "Run a task by id",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, repoRoot, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()

			if !git.Available(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			cfg, err := loadConfig(repoRoot)
			if err != nil {
				return err
			}

			tracker := task.NewBeadsTracker("")
			runStore := db.NewStore(storeDB)
			pdcaFactory := pdca.NewAgentFactory(cfg, runStore, tracker)
			runner, err := run.NewADKRunner(repoRoot, cfg, runStore, tracker, pdcaFactory)
			if err != nil {
				return err
			}
			normaDir := filepath.Join(repoRoot, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			id := args[0]
			return runTaskByID(cmd.Context(), tracker, runStore, runner, id)
		},
	}
	return cmd
}
