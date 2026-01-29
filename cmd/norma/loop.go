package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func loopCmd() *cobra.Command {
	var continueOnFail bool
	var activeFeatureID string
	var activeEpicID string
	cmd := &cobra.Command{
		Use:          "loop",
		Short:        "Run tasks one by one from the tracker",
		Long:         "Run tasks one by one from the tracker using the scheduler when no task id is provided.",
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
			runner, err := run.NewRunner(repoRoot, cfg, runStore, tracker)
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
			return runTasks(cmd.Context(), tracker, runStore, runner, continueOnFail, policy)
		},
	}
	cmd.Flags().BoolVar(&continueOnFail, "continue", false, "continue running ready tasks after a failure")
	cmd.Flags().StringVar(&activeFeatureID, "active-feature", "", "prefer ready issues under this feature id")
	cmd.Flags().StringVar(&activeEpicID, "active-epic", "", "prefer ready issues under this epic id")
	return cmd
}

func normalizeAC(texts []string) []task.AcceptanceCriterion {
	if len(texts) == 0 {
		return nil
	}
	out := make([]task.AcceptanceCriterion, 0, len(texts))
	for i, text := range texts {
		id := fmt.Sprintf("AC%d", i+1)
		out = append(out, task.AcceptanceCriterion{ID: id, Text: text})
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
	selectedProfile, agents, err := cfg.ResolveAgents(viper.GetString("profile"))
	if err != nil {
		return config.Config{}, err
	}
	cfg.Profile = selectedProfile
	cfg.Agents = agents
	if cfg.Budgets.MaxIterations <= 0 {
		return config.Config{}, fmt.Errorf("budgets.max_iterations must be > 0")
	}
	return cfg, nil
}
