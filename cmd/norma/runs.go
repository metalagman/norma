package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/run"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func runsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Manage norma runs",
	}
	cmd.AddCommand(runsPruneCmd())
	return cmd
}

func runsPruneCmd() *cobra.Command {
	var keepLast int
	var keepDays int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune old runs from disk and database",
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

			policy := run.RetentionPolicy{KeepLast: keepLast, KeepDays: keepDays}
			if policy.KeepLast <= 0 && policy.KeepDays <= 0 {
				policy = run.RetentionPolicy{
					KeepLast: cfg.Retention.KeepLast,
					KeepDays: cfg.Retention.KeepDays,
				}
			}
			if policy.KeepLast <= 0 && policy.KeepDays <= 0 {
				return fmt.Errorf("set --keep-last or --keep-days (or configure retention in .norma/config.json)")
			}

			normaDir := filepath.Join(repoRoot, ".norma")
			lock, err := run.AcquireRunLock(normaDir)
			if err != nil {
				return err
			}
			defer lock.Release()

			res, err := run.PruneRuns(context.Background(), storeDB, filepath.Join(normaDir, "runs"), policy, dryRun)
			if err != nil {
				return err
			}
			mode := "deleted"
			if dryRun {
				mode = "would delete"
			}
			log.Info().Msgf("%s %d runs (kept %d, skipped %d)", mode, res.Deleted, res.Kept, res.Skipped)
			return nil
		},
	}
	cmd.Flags().IntVar(&keepLast, "keep-last", 0, "keep the newest N runs")
	cmd.Flags().IntVar(&keepDays, "keep-days", 0, "keep runs newer than N days")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be pruned without deleting")
	return cmd
}
