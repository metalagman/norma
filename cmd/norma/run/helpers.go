package runcmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/normahq/norma/v2/internal/config"
	"github.com/normahq/norma/v2/internal/db"
	"github.com/normahq/norma/v2/internal/run"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

const (
	statusFailed  = "failed"
	statusStopped = "stopped"
	statusPassed  = "passed"
	statusDoing   = "doing"
	statusTodo    = "todo"
)

func openDB(ctx context.Context) (*sql.DB, string, func(), error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, "", func() {}, err
	}
	normaDir := filepath.Join(workingDir, ".norma")
	if err := os.MkdirAll(normaDir, 0o700); err != nil {
		return nil, "", func() {}, err
	}
	dbPath := filepath.Join(normaDir, "norma.db")
	storeDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, "", func() {}, err
	}
	return storeDB, workingDir, func() { _ = storeDB.Close() }, nil
}

func loadRuntimeAndCLIConfig(workingDir string) (config.Config, config.CLISettings, error) {
	cfg, appCfg, err := config.LoadRuntimeAndCLIConfig(config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	})
	if err != nil {
		return config.Config{}, config.CLISettings{}, err
	}
	if appCfg.EffectiveBudgets().MaxIterations <= 0 {
		return config.Config{}, config.CLISettings{}, fmt.Errorf("cli.budgets.max_iterations must be > 0")
	}
	return cfg, appCfg, nil
}

func runTaskByID(ctx context.Context, tracker task.Tracker, runStore *db.Store, runner *run.Runner, id string) error {
	item, err := tracker.Task(ctx, id)
	if err != nil {
		return err
	}
	switch item.Status {
	case statusTodo, statusFailed, statusStopped:
	case statusDoing:
		if item.RunID != nil {
			status, err := runStore.RunStatus(ctx, *item.RunID)
			if err != nil {
				return err
			}
			if status == "running" {
				return fmt.Errorf("task %s already running", id)
			}
		}
		if err := tracker.MarkStatus(ctx, id, statusFailed); err != nil {
			return err
		}
	default:
		return fmt.Errorf("task %s status is %s", id, item.Status)
	}
	if err := tracker.MarkStatus(ctx, id, "planning"); err != nil {
		return err
	}
	result, err := runner.Run(ctx, item.Goal, item.Criteria, id)
	if err != nil {
		_ = tracker.MarkStatus(ctx, id, statusFailed)
		return err
	}
	if result.RunID != "" {
		_ = tracker.SetRun(ctx, id, result.RunID)
	}
	switch result.Status {
	case statusPassed:
		fmt.Printf("task %s passed (run %s)\n", id, result.RunID)
		return nil
	case statusFailed:
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	case statusStopped:
		return fmt.Errorf("task %s stopped (run %s)", id, result.RunID)
	default:
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	}
}

func recoverDoingTasks(ctx context.Context, tracker task.Tracker, runStore *db.Store, normaDir string) error {
	lock, ok, err := run.TryAcquireRunLock(normaDir)
	if err != nil {
		return err
	}
	if ok {
		defer func() {
			if lErr := lock.Release(); lErr != nil {
				log.Warn().Err(lErr).Msg("failed to release run lock")
			}
		}()
	}
	status := statusDoing
	items, err := tracker.List(ctx, &status)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.RunID == nil {
			if err := tracker.MarkStatus(ctx, item.ID, statusFailed); err != nil {
				return err
			}
			continue
		}
		runStatus, err := runStore.RunStatus(ctx, *item.RunID)
		if err != nil {
			return err
		}
		if runStatus != "running" || ok {
			if err := tracker.MarkStatus(ctx, item.ID, statusFailed); err != nil {
				return err
			}
		}
	}
	return nil
}
