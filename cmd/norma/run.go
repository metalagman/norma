package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func runCmd() *cobra.Command {
	var runLeaves bool
	var continueOnFail bool
	cmd := &cobra.Command{
		Use:          "run [task-id]",
		Short:        "Run a task by id or run leaf tasks",
		Long:         "Run a task by id. If no task id is provided, run all leaf tasks in dependency order.",
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				runLeaves = true
				return nil
			}
			if runLeaves {
				if len(args) != 0 {
					return fmt.Errorf("no task id allowed with --leaf")
				}
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
			runner, err := run.NewRunner(repoRoot, cfg, runStore)
			if err != nil {
				return err
			}
			normaDir := filepath.Join(repoRoot, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			if runLeaves {
				return runLeafTasks(cmd.Context(), tracker, runStore, runner, continueOnFail)
			}
			id := args[0]
			if err := runTaskByID(cmd.Context(), tracker, runStore, runner, id); err != nil {
				if continueOnFail {
					fmt.Println(err)
					return nil
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&runLeaves, "leaf", false, "run all leaf tasks")
	cmd.Flags().BoolVar(&continueOnFail, "continue", false, "continue running leaf tasks after a failure")
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
