package runscmd

import (
	"fmt"
	"path/filepath"

	"github.com/normahq/norma/internal/run"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command builds the `norma runs` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "Manage norma runs",
	}
	cmd.AddCommand(pruneCommand())
	return cmd
}

func pruneCommand() *cobra.Command {
	var keepLast int
	var keepDays int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune old runs from disk and database",
		RunE: func(cmd *cobra.Command, _ []string) error {
			storeDB, workingDir, closeFn, err := openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer closeFn()

			_, cliCfg, err := loadRuntimeAndCLIConfig(workingDir)
			if err != nil {
				return err
			}

			policy := run.RetentionPolicy{KeepLast: keepLast, KeepDays: keepDays}
			if policy.KeepLast <= 0 && policy.KeepDays <= 0 {
				effective := cliCfg.EffectiveRetention()
				policy = run.RetentionPolicy{
					KeepLast: effective.KeepLast,
					KeepDays: effective.KeepDays,
				}
			}
			if policy.KeepLast <= 0 && policy.KeepDays <= 0 {
				return fmt.Errorf("set --keep-last or --keep-days (or configure cli.retention in .norma/config.yaml or .norma/cli.yaml)")
			}

			normaDir := filepath.Join(workingDir, ".norma")
			lock, err := run.AcquireRunLock(normaDir)
			if err != nil {
				return err
			}
			defer func() {
				if lErr := lock.Release(); lErr != nil {
					log.Fatal().Err(lErr).Msg("failed to release run lock")
				}
			}()

			res, err := run.PruneRuns(cmd.Context(), storeDB, filepath.Join(normaDir, "runs"), policy, dryRun)
			if err != nil {
				return err
			}
			mode := "deleted"
			if dryRun {
				mode = "would delete"
			}
			log.Info().
				Int("deleted", res.Deleted).
				Int("kept", res.Kept).
				Int("skipped", res.Skipped).
				Msg(mode + " runs")
			return nil
		},
	}
	cmd.Flags().IntVar(&keepLast, "keep-last", 0, "keep the newest N runs")
	cmd.Flags().IntVar(&keepDays, "keep-days", 0, "keep runs newer than N days")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be pruned without deleting")
	return cmd
}
