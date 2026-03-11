package plancmd

import (
	"fmt"
	"os"

	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/planner"
	"github.com/spf13/cobra"
)

func tuiCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactively decompose an epic into features and tasks and persist them to Beads",
		Args:  cobra.NoArgs,
		RunE:  runTUI,
	}
	return cmd
}

func runTUI(cmd *cobra.Command, _ []string) error {
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

	req := planner.Request{
		Mode: planner.ModeWizard,
	}

	plannerID, ok := cfg.RoleIDs["planner"]
	if !ok {
		return fmt.Errorf("planner agent not configured in selected profile %q", cfg.Profile)
	}
	return runAgentPlanner(cmd, repoRoot, cfg.Agents, plannerID, req)
}
