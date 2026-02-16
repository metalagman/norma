package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/planner"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func planCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "plan <epic-description>",
		Short:        "Decompose an epic into features and tasks and persist them to Beads",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}
			if !git.Available(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			req := planner.Request{
				EpicDescription: strings.TrimSpace(strings.Join(args, " ")),
			}

			rawCfg, err := loadRawConfig(repoRoot)
			if err != nil {
				return err
			}
			plannerAgentCfg, err := resolvePlannerAgent(rawCfg)
			if err != nil {
				return err
			}

			llmPlanner, err := planner.NewLLMPlanner(repoRoot, plannerAgentCfg)
			if err != nil {
				return err
			}

			plan, runDir, err := llmPlanner.Generate(cmd.Context(), req)
			if err != nil {
				return err
			}

			beadsTool := planner.NewBeadsTool(task.NewBeadsTracker(""))
			applied, err := beadsTool.Apply(cmd.Context(), plan)
			if err != nil {
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
			return nil
		},
	}

	return cmd
}

func resolvePlannerAgent(cfg config.Config) (config.AgentConfig, error) {
	selectedProfile := viper.GetString("profile")

	_, resolvedAgents, err := cfg.ResolveAgents(selectedProfile)
	if err != nil {
		return config.AgentConfig{}, err
	}

	if plannerCfg, ok := resolvedAgents["planner"]; ok {
		return plannerCfg, nil
	}

	return config.AgentConfig{}, fmt.Errorf("resolved configuration is missing a 'planner' agent")
}
