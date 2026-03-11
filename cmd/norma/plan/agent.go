package plancmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/planner"
	"github.com/spf13/cobra"
)

func runAgentPlanner(cmd *cobra.Command, repoRoot string, registry map[string]config.AgentConfig, plannerID string, req planner.Request) error {
	p := planner.NewAgentPlanner(repoRoot, registry, plannerID)
	runDir, err := p.RunInteractive(cmd.Context(), req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, planner.ErrHandledInTUI) {
			return nil
		}
		return err
	}

	fmt.Printf("\nPlanner session complete.\n")
	fmt.Printf("Planning run directory: %s\n", runDir)
	return nil
}
