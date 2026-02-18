package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/planner"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func planCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan [epic goal]",
		Short: "Interactively decompose an epic into features and tasks and persist them to Beads",
		Args:  cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}
			if !git.Available(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			rawCfg, err := loadRawConfig(repoRoot)
			if err != nil {
				return err
			}

			epicDescription := ""
			if len(args) > 0 {
				epicDescription = args[0]
			} else {
				fmt.Print("What do you want to build? Please describe the project goal (the epic): ")
				var scanner = bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					epicDescription = scanner.Text()
				}
			}

			if epicDescription == "" {
				return fmt.Errorf("epic description is required")
			}

			req := planner.Request{
				EpicDescription: epicDescription,
			}

			app := fx.New(
				fx.Provide(func() context.Context { return cmd.Context() }),
				fx.Supply(repoRoot),
				fx.Supply(rawCfg),
				fx.Supply(req),
				fx.Provide(planner.ToFactoryConfig),
				modelfactory.Module,
				task.Module,
				planner.Module,
				fx.Invoke(runPlan),
				fx.NopLogger,
			)

			return app.Start(cmd.Context())
		},
	}

	return cmd
}

func runPlan(
	ctx context.Context,
	p *planner.LLMPlanner,
	bt *planner.BeadsTool,
	req planner.Request,
	shutdown fx.Shutdowner,
) error {
	plan, runDir, err := p.Generate(ctx, req)
	if err != nil {
		_ = shutdown.Shutdown()
		return err
	}

	applied, err := bt.Apply(ctx, plan)
	if err != nil {
		_ = shutdown.Shutdown()
		return err
	}

	fmt.Printf("\nPlan generated and persisted to Beads.\n")
	fmt.Printf("Epic: %s\n", applied.EpicID)
	for i, feature := range applied.Features {
		fmt.Printf("Feature %d: %s\n", i+1, feature.FeatureID)
		for _, taskID := range feature.TaskIDs {
			fmt.Printf("  - Task: %s\n", taskID)
		}
	}
	fmt.Printf("Planning artifacts: %s\n", runDir)

	return shutdown.Shutdown()
}
