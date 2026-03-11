package plancmd

import (
	"fmt"
	"io"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/session"

	"github.com/metalagman/norma/internal/adk/agentfactory"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/planner"
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
			plannerCfg, ok := cfg.Agents["planner"]
			if !ok {
				return fmt.Errorf("planner agent not configured in selected profile %q", cfg.Profile)
			}
			factory := agentfactory.NewFactory(map[string]config.AgentConfig{
				"planner": plannerCfg,
			})
			creationReq := agentfactory.CreationRequest{
				Name:              "NormaPlannerAgent",
				Description:       "Norma planner via generic agent runtime",
				SystemInstruction: planner.PlannerInstruction(),
				WorkingDirectory:  repoRoot,
				Stderr:            io.Discard,
				PermissionHandler: planner.PlannerPermissionHandler,
			}
			plannerDebugAgent, newErr := factory.CreateAgent(cmd.Context(), "planner", creationReq)
			if newErr != nil {
				return fmt.Errorf("create planner runtime: %w", newErr)
			}
			if closer, ok := plannerDebugAgent.(interface{ Close() error }); ok {
				defer func() { _ = closer.Close() }()
			}

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
