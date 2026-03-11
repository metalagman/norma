package plancmd

import (
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/session"

	"github.com/spf13/cobra"
)

func webCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "web [api|a2a|webui ...]",
		Short:              "Run the planner agent with the ADK web launcher",
		SilenceUsage:       true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			cfg, err := loadConfig(repoRoot)
			if err != nil {
				return err
			}
			plannerID, ok := cfg.RoleIDs["planner"]
			if !ok {
				return fmt.Errorf("planner agent not configured in selected profile %q", cfg.Profile)
			}
			plannerDebugAgent, closePlannerAgent, err := createPlannerAgent(cmd.Context(), repoRoot, cfg.Agents, plannerID)
			if err != nil {
				return err
			}
			defer func() { _ = closePlannerAgent() }()

			launchCfg := &launcher.Config{
				AgentLoader:     adkagent.NewSingleLoader(plannerDebugAgent),
				SessionService:  session.InMemoryService(),
				ArtifactService: artifact.InMemoryService(),
			}

			launcherArgs := make([]string, 0, len(args)+1)
			launcherArgs = append(launcherArgs, "web")
			if len(args) == 0 {
				launcherArgs = append(launcherArgs, "api", "webui")
			} else {
				launcherArgs = append(launcherArgs, args...)
			}

			l := full.NewLauncher()
			if err := l.Execute(cmd.Context(), launchCfg, launcherArgs); err != nil {
				return fmt.Errorf("run planner web launcher: %w\n\n%s", err, l.CommandLineSyntax())
			}
			return nil
		},
	}
}
