package plancmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/planner"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
)

// Command builds the `norma plan` command group.
func Command() *cobra.Command {
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

			cfg, err := loadConfig(repoRoot)
			if err != nil {
				return err
			}

			epicDescription := ""
			if len(args) > 0 {
				epicDescription = args[0]
			}
			req := planner.Request{
				EpicDescription: epicDescription,
				Mode:            planner.ModeWizard,
			}

			plannerCfg, ok := cfg.Agents["planner"]
			if !ok {
				return fmt.Errorf("planner agent not configured in selected profile %q", cfg.Profile)
			}
			if config.IsACPType(plannerCfg.Type) {
				return runACPPlanner(cmd, repoRoot, plannerCfg, req)
			}
			return runLLMPlanner(cmd, repoRoot, cfg, req)
		},
	}

	cmd.AddCommand(webCommand())
	return cmd
}

func runLLMPlanner(cmd *cobra.Command, repoRoot string, cfg config.Config, req planner.Request) error {
	factory := modelfactory.NewFactory(modelfactory.FactoryConfig(cfg.Agents))
	m, err := factory.CreateModel("planner")
	if err != nil {
		return fmt.Errorf("create planner model %q: %w", "planner", err)
	}
	p, err := planner.NewLLMPlanner(repoRoot, m)
	if err != nil {
		return fmt.Errorf("create planner runtime: %w", err)
	}
	bt := planner.NewBeadsTool(task.NewBeadsTracker(""))

	ctx := cmd.Context()
	plan, runDir, err := p.Generate(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, planner.ErrHandledInTUI) {
			return nil
		}
		return err
	}

	applied, err := bt.Apply(ctx, plan)
	if err != nil {
		return err
	}

	fmt.Printf("\nPlan confirmed and artifacts tracked: %s\n", applied.EpicID)
	fmt.Printf("Planning run directory: %s\n", runDir)
	return nil
}
