package runcmd

import (
	"fmt"
	"path/filepath"

	"github.com/normahq/norma/v2/internal/agents/pdca"
	"github.com/normahq/norma/v2/internal/db"
	"github.com/normahq/norma/v2/internal/git"
	"github.com/normahq/norma/v2/internal/run"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/spf13/cobra"
)

// Command builds the `norma run` command.
func Command() *cobra.Command {
	return &cobra.Command{
		Use:          "run <task-id>",
		Short:        "Run a task by id",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			storeDB, workingDir, closeFn, err := openDB(cmd.Context())
			if err != nil {
				return err
			}
			defer closeFn()

			if !git.Available(cmd.Context(), workingDir) {
				return fmt.Errorf("current directory is not a git repository")
			}

			cfg, cliCfg, err := loadRuntimeAndCLIConfig(workingDir)
			if err != nil {
				return err
			}

			tracker := task.NewBeadsTracker("")
			runStore := db.NewStore(storeDB)
			pdcaFactory := pdca.NewFactory(cfg, cliCfg.EffectiveBudgets().MaxIterations, runStore, tracker)
			runner, err := run.NewADKRunner(workingDir, cfg, runStore, tracker, pdcaFactory)
			if err != nil {
				return err
			}
			normaDir := filepath.Join(workingDir, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			return runTaskByID(cmd.Context(), tracker, runStore, runner, args[0])
		},
	}
}
