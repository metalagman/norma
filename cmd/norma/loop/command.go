package loopcmd

import (
	"fmt"
	"path/filepath"

	"github.com/normahq/norma/v2/internal/adkrunner"
	"github.com/normahq/norma/v2/internal/agents/normaloop"
	"github.com/normahq/norma/v2/internal/agents/pdca"
	"github.com/normahq/norma/v2/internal/db"
	"github.com/normahq/norma/v2/internal/git"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command builds the `norma loop` command.
func Command() *cobra.Command {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			normaDir := filepath.Join(workingDir, ".norma")
			if err := recoverDoingTasks(cmd.Context(), tracker, runStore, normaDir); err != nil {
				return err
			}

			policy := task.SelectionPolicy{
				ActiveFeatureID: activeFeatureID,
				ActiveEpicID:    activeEpicID,
			}
			loopAgent, err := normaloop.New(normaloop.Config{
				Logger:         log.Logger,
				Cfg:            cfg,
				WorkingDir:     workingDir,
				Tracker:        tracker,
				RunStore:       runStore,
				Factory:        pdcaFactory,
				ContinueOnFail: continueOnFail,
				Policy:         policy,
			})
			if err != nil {
				return err
			}

			log.Info().Msg("Running tasks using Google ADK Loop Agent...")
			_, _, err = adkrunner.Run(cmd.Context(), adkrunner.RunInput{
				AppName: "norma",
				UserID:  "norma-user",
				Agent:   loopAgent,
				InitialState: map[string]any{
					"iteration": 1,
				},
			})
			return err
		},
	}
	cmd.Flags().BoolVar(&continueOnFail, "continue", false, "deprecated no-op; loop continues after runtime failures by default")
	_ = cmd.Flags().MarkDeprecated("continue", "loop continues after runtime failures by default")
	cmd.Flags().StringVar(&activeFeatureID, "active-feature", "", "prefer ready issues under this feature id")
	cmd.Flags().StringVar(&activeEpicID, "active-epic", "", "prefer ready issues under this epic id")
	return cmd
}
