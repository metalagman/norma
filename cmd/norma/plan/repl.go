package plancmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/normahq/norma/v2/internal/apps/agentrepl"
	"github.com/normahq/norma/v2/internal/git"
	"github.com/spf13/cobra"
	adkagent "google.golang.org/adk/v2/agent"
)

const (
	plannerREPLAppName  = "norma-plan-repl"
	plannerREPLUserID   = "norma-plan-repl-user"
	plannerREPLIntroMsg = "What do you want to plan?"
)

func replCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "repl",
		Short:   "Run the planner in a line-based REPL",
		Long:    "Run the planner agent in an interactive line-based Read-Eval-Print Loop (REPL). The REPL allows you to issue planning commands and receive agent responses in a simple terminal interface.",
		Example: "  codex plan repl",
		Args:    cobra.NoArgs,
		RunE:    runREPL,
	}
}

func runREPL(cmd *cobra.Command, _ []string) error {
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

	if err := printPlannerREPLIntro(cmd.OutOrStdout()); err != nil {
		return err
	}

	return agentrepl.RunAgentREPL(cmd.Context(), plannerREPLConfig(cmd, workingDir, plannerCfg))
}

func printPlannerREPLIntro(stdout io.Writer) error {
	_, err := fmt.Fprintln(stdout, plannerREPLIntroMsg)
	return err
}

func plannerREPLConfig(cmd *cobra.Command, workingDir string, cfg plannerRuntimeConfig) agentrepl.AgentREPLConfig {
	return agentrepl.AgentREPLConfig{
		AppName: plannerREPLAppName,
		UserID:  plannerREPLUserID,
		Stdin:   cmd.InOrStdin(),
		Stdout:  cmd.OutOrStdout(),
		Stderr:  cmd.ErrOrStderr(),
		AgentFactory: func(
			ctx context.Context,
			permissionHandler agentrepl.PermissionHandler,
			stderr io.Writer,
		) (adkagent.Agent, func() error, error) {
			return createPlannerAgentWithOptions(ctx, workingDir, cfg, plannerAgentCreateOptions{
				Stderr:            stderr,
				PermissionHandler: permissionHandler,
			})
		},
	}
}
