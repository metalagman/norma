package swarmcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/normahq/norma/internal/agents/swarm"
	"github.com/normahq/norma/internal/task"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Command builds the `norma swarm` command.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "swarm <epic-id>",
		Short:        "Run the assignee-routed swarm harness for one Beads epic",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current working directory: %w", err)
			}
			workingDir, err = filepath.Abs(workingDir)
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}

			runtimeCfg, cliCfg, err := loadRuntimeAndCLIConfigUnresolved(workingDir)
			if err != nil {
				return err
			}
			roles, err := runtimeCfg.ResolveSwarmRoles(cliCfg)
			if err != nil {
				return err
			}

			tracker := task.NewBeadsTracker("")
			tracker.WorkingDir = workingDir

			runtime, err := swarm.New(cmd.Context(), swarm.Config{
				Logger:     log.Logger,
				WorkingDir: workingDir,
				Runtime:    runtimeCfg,
				Roles:      roles,
				Tracker:    tracker,
			})
			if err != nil {
				return err
			}
			defer func() {
				if err := runtime.Close(); err != nil {
					log.Warn().Err(err).Msg("failed to close swarm runtime")
				}
			}()

			return runtime.Run(cmd.Context(), args[0])
		},
	}
	return cmd
}
