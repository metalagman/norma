package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/metalagman/norma/internal/workflows/pdca"
	"github.com/spf13/cobra"
)

func loopCmd() *cobra.Command {
	var continueOnFail bool
	var activeFeatureID string
	var activeEpicID string
	cmd := &cobra.Command{
		Use:          "loop",
		Aliases:      []string{"loopadk"},
		Short:        "Run tasks one by one using Google ADK Loop Agent",
		Long:         "Run tasks one by one from the tracker using Google ADK Loop Agent for orchestration.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
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

			pdcaWorkflow := pdca.NewWorkflow(cfg, runStore, tracker)
			taskRunner, err := run.NewADKRunner(repoRoot, cfg, runStore, tracker, pdcaWorkflow)
			if err != nil {
				return err
			}

			normaDir := filepath.Join(repoRoot, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			policy := task.SelectionPolicy{
				ActiveFeatureID: activeFeatureID,
				ActiveEpicID:    activeEpicID,
			}
			loopWorkflow := normaloop.NewWorkflow(tracker, runStore, taskRunner, continueOnFail, policy)
			fmt.Println("Running tasks using Google ADK Loop Agent...")
			return loopWorkflow.Run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&continueOnFail, "continue", false, "continue running ready tasks after a failure")
	cmd.Flags().StringVar(&activeFeatureID, "active-feature", "", "prefer ready issues under this feature id")
	cmd.Flags().StringVar(&activeEpicID, "active-epic", "", "prefer ready issues under this epic id")
	return cmd
}
