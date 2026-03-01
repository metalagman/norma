package main

import (
	"encoding/json"
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/metalagman/norma/internal/planner"
	"github.com/metalagman/norma/internal/planner/llmtools"
	"github.com/spf13/cobra"
)

func planWebCmd() *cobra.Command {
	cmd := &cobra.Command{
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

			factoryCfg := make(modelfactory.FactoryConfig, len(cfg.Agents))
			for name, agentCfg := range cfg.Agents {
				factoryCfg[name] = agentCfg
			}
			factory := modelfactory.NewFactory(factoryCfg)

			modelName := "planner"
			if _, ok := cfg.Agents[modelName]; !ok {
				modelName = "plan"
			}
			m, err := factory.CreateModel(modelName)
			if err != nil {
				return fmt.Errorf("create planner model %q: %w", modelName, err)
			}

			humanTool, err := llmtools.NewHumanTool(func(_ string) (string, error) {
				// Web UI chat is the interactive channel in launcher mode.
				return "No additional clarification provided.", nil
			})
			if err != nil {
				return fmt.Errorf("create human tool: %w", err)
			}

			shellTool, err := llmtools.NewShellCommandTool(repoRoot)
			if err != nil {
				return fmt.Errorf("create shell tool: %w", err)
			}

			beadsTool, err := llmtools.NewBeadsCommandTool(repoRoot, func(tctx tool.Context, rawPlan json.RawMessage) (string, error) {
				var dec planner.Decomposition
				if err := json.Unmarshal(rawPlan, &dec); err != nil {
					return "", fmt.Errorf("unmarshal decomposition: %w", err)
				}
				if err := dec.Validate(); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
				if err := tctx.State().Set("decomposition", dec); err != nil {
					return "", fmt.Errorf("failed to set decomposition in state: %w", err)
				}
				return "Plan confirmed and staged for persistence. You can now finish the session.", nil
			})
			if err != nil {
				return fmt.Errorf("create beads tool: %w", err)
			}

			plannerDebugAgent, err := llmagent.New(llmagent.Config{
				Name:        "NormaPlanner",
				Model:       m,
				Description: "Interactive Norma planning agent that decomposes epics into features and tasks.",
				Tools:       []tool.Tool{humanTool, shellTool, beadsTool},
				Instruction: planner.PlannerInstruction(),
			})
			if err != nil {
				return fmt.Errorf("create planner debug agent: %w", err)
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
	return cmd
}
