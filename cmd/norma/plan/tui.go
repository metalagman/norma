package plancmd

import (
	"fmt"
	"os"

	"github.com/normahq/norma/internal/git"
	"github.com/spf13/cobra"
)

func tuiCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tui",
		Short:   "Launch the interactive TUI planner",
		Long:    "Launch an interactive terminal UI (TUI) for decomposing epics into features and tasks. The TUI provides a visual, step-by-step workflow for planning work with AI agent assistance.",
		Example: "  codex plan tui",
		Args:    cobra.NoArgs,
		RunE:    runTUI,
	}
	return cmd
}

func runTUI(cmd *cobra.Command, _ []string) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if !git.Available(cmd.Context(), workingDir) {
		return fmt.Errorf("current directory is not a git repository")
	}

	plannerCfg, err := loadPlannerRuntimeConfig(workingDir)
	if err != nil {
		return err
	}
	return runAgentPlanner(cmd, workingDir, plannerCfg)
}
