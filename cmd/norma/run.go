package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func runCmd() *cobra.Command {
	var continueOnFail bool
	var activeFeatureID string
	var activeEpicID string
	cmd := &cobra.Command{
		Use:          "run [task-id]",
		Short:        "Run a task by id or run the next ready task",
		Long:         "Run a task by id. If no task id is provided, run the next ready task chosen by the scheduler.",
		SilenceUsage: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("task id is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, repoRoot, closeFn, err := openDB()
			if err != nil {
				return err
			}
			defer closeFn()
			cfg, err := loadConfig(repoRoot)
			if err != nil {
				return err
			}

			tracker := task.NewBeadsTracker("")
			runStore := run.NewStore(storeDB)
			runner, err := run.NewRunner(repoRoot, cfg, runStore, tracker)
			if err != nil {
				return err
			}
			normaDir := filepath.Join(repoRoot, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			if len(args) == 0 {
				policy := task.SelectionPolicy{
					ActiveFeatureID: activeFeatureID,
					ActiveEpicID:    activeEpicID,
				}
				return runLeafTasks(cmd.Context(), tracker, runStore, runner, continueOnFail, policy)
			}
			id := args[0]
			if err := runTaskByID(cmd.Context(), tracker, runStore, runner, id); err != nil {
				if continueOnFail {
					log.Error().Err(err).Msg("task failed")
					return nil
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&continueOnFail, "continue", false, "continue running ready tasks after a failure")
	cmd.Flags().StringVar(&activeFeatureID, "active-feature", "", "prefer ready issues under this feature id")
	cmd.Flags().StringVar(&activeEpicID, "active-epic", "", "prefer ready issues under this epic id")
	return cmd
}

func normalizeAC(texts []string) []model.AcceptanceCriterion {
	if len(texts) == 0 {
		return nil
	}
	out := make([]model.AcceptanceCriterion, 0, len(texts))
	for i, text := range texts {
		id := fmt.Sprintf("AC%d", i+1)
		out = append(out, model.AcceptanceCriterion{ID: id, Text: text})
	}
	return out
}

func loadConfig(repoRoot string) (config.Config, error) {
	path := viper.GetString("config")
	if path == "" {
		path = filepath.Join(".norma", "config.json")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	viper.SetConfigFile(path)
	viper.SetConfigType("json")
	if err := viper.ReadInConfig(); err != nil {
		return config.Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Budgets.MaxIterations <= 0 {
		return config.Config{}, fmt.Errorf("budgets.max_iterations must be > 0")
	}
	return cfg, nil
}
