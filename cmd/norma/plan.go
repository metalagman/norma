package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/planner"
	"github.com/metalagman/norma/internal/task"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	backlogRefinerFeature = "backlog_refiner"
	featurePlannerAgent   = "planner"
)

func planCmd() *cobra.Command {
	var mode string

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

			reqMode := strings.ToLower(strings.TrimSpace(mode))
			if reqMode == "" {
				reqMode = planner.ModeWizard
			}
			if reqMode != planner.ModeWizard && reqMode != planner.ModeAuto {
				return fmt.Errorf("unsupported mode %q (allowed: wizard, auto)", reqMode)
			}

			req := planner.Request{
				EpicDescription: strings.TrimSpace(strings.Join(args, " ")),
				Mode:            reqMode,
			}
			if reqMode == planner.ModeWizard {
				if !stdinIsTerminal() {
					return fmt.Errorf("wizard mode requires an interactive terminal; use --mode auto in non-interactive environments")
				}
				clarifications, err := collectWizardClarifications(os.Stdin, os.Stdout)
				if err != nil {
					return err
				}
				req.Clarifications = clarifications
			}

			rawCfg, err := loadRawConfig(repoRoot)
			if err != nil {
				return err
			}
			plannerAgentCfg, err := resolvePlannerAgent(rawCfg)
			if err != nil {
				return err
			}

			execPlanner, err := planner.NewExecPlanner(repoRoot, plannerAgentCfg)
			if err != nil {
				return err
			}

			plan, runDir, err := execPlanner.Generate(cmd.Context(), req)
			if err != nil {
				return err
			}

			beadsTool := planner.NewBeadsTool(task.NewBeadsTracker(""))
			applied, err := beadsTool.Apply(cmd.Context(), plan)
			if err != nil {
				return err
			}

			fmt.Printf("Plan generated and persisted to Beads.\n")
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

	cmd.Flags().StringVar(&mode, "mode", planner.ModeWizard, "planning mode: wizard or auto")
	return cmd
}

func resolvePlannerAgent(cfg config.Config) (config.AgentConfig, error) {
	selectedProfile := viper.GetString("profile")

	_, featureAgents, featureErr := cfg.ResolveFeatureAgents(selectedProfile, backlogRefinerFeature)
	if featureErr == nil {
		if plannerCfg, ok := featureAgents[featurePlannerAgent]; ok {
			return plannerCfg, nil
		}
	}

	_, pdcaAgents, err := cfg.ResolveAgents(selectedProfile)
	if err != nil {
		if featureErr != nil {
			return config.AgentConfig{}, fmt.Errorf("resolve planner agent: %v (feature fallback failed: %w)", err, featureErr)
		}
		return config.AgentConfig{}, err
	}

	plannerCfg, ok := pdcaAgents["plan"]
	if !ok {
		return config.AgentConfig{}, fmt.Errorf("resolved PDCA configuration is missing plan agent")
	}
	return plannerCfg, nil
}

func collectWizardClarifications(in io.Reader, out io.Writer) ([]planner.Clarification, error) {
	reader := bufio.NewReader(in)
	questions := []string{
		"Epic title hint (optional)",
		"Primary users/personas (optional)",
		"Constraints or non-goals (optional)",
		"Verification expectations (optional)",
		"Additional context (optional)",
	}

	answers := make([]planner.Clarification, 0, len(questions))
	if _, err := fmt.Fprintln(out, "Wizard mode: answer a few questions to clarify planning scope."); err != nil {
		return nil, fmt.Errorf("write wizard prompt: %w", err)
	}
	for _, question := range questions {
		if _, err := fmt.Fprintf(out, "%s: ", question); err != nil {
			return nil, fmt.Errorf("write wizard question: %w", err)
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read wizard input: %w", err)
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		answers = append(answers, planner.Clarification{
			Question: question,
			Answer:   answer,
		})
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return answers, nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
