package main

import (
	"context"
	"errors"
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
		Use:          "plan [epic goal]",
		Short:        "Interactively decompose an epic into features and tasks and persist them to Beads",
		Args:         cobra.MaximumNArgs(1),
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

	cmd.AddCommand(planWebCmd())

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
		if errors.Is(err, context.Canceled) {
			return nil
		}
		if errors.Is(err, planner.ErrHandledInTUI) {
			return nil
		}
		return err
	}

	// Check if the plan was already persisted by the agent (by checking if the Epic exists in Beads)
	// For now, we assume if we have a plan, we should apply it unless Apply fails with "already exists"
	// but Beads create doesn't have idempotency check by title easily.
	// We'll just call Apply and let it create. If the agent already did it, we might get duplicates,
	// but the agent is instructed to only output JSON *after* creating.
	// Actually, the best way is to check if the agent created any IDs.
	// For MVP, we'll just call Apply.

	applied, err := bt.Apply(ctx, plan)
	if err != nil {
		// Log error but don't fail if it's just about persistence?
		// No, Apply error should be handled.
		_ = shutdown.Shutdown()
		return err
	}

	fmt.Printf("\nPlan confirmed and artifacts tracked: %s\n", applied.EpicID)
	fmt.Printf("Planning run directory: %s\n", runDir)

	return shutdown.Shutdown()
}
