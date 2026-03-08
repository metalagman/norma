package plancmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/acpagent"
	normaagent "github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/planner"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func runACPPlanner(cmd *cobra.Command, repoRoot string, plannerCfg config.AgentConfig, req planner.Request) error {
	acpCmd, err := normaagent.ResolveACPCommand(plannerCfg)
	if err != nil {
		return err
	}

	acpRuntime, err := acpagent.New(acpagent.Config{
		Context:           cmd.Context(),
		Name:              "NormaPlannerACP",
		Description:       "Norma planner via ACP runtime",
		Command:           acpCmd,
		WorkingDir:        repoRoot,
		Stderr:            io.Discard,
		PermissionHandler: allowACPPermissions,
		Logger:            acpNoopLogger(),
	})
	if err != nil {
		return fmt.Errorf("create ACP planner runtime: %w", err)
	}
	defer func() { _ = acpRuntime.Close() }()

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma-plan-acp",
		Agent:          acpRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		return fmt.Errorf("create planner runner: %w", err)
	}
	sess, err := sessionService.Create(cmd.Context(), &session.CreateRequest{
		AppName: "norma-plan-acp",
		UserID:  "norma-planner-user",
	})
	if err != nil {
		return fmt.Errorf("create planner session: %w", err)
	}

	if goal := strings.TrimSpace(req.EpicDescription); goal != "" {
		initialPrompt := strings.TrimSpace(fmt.Sprintf(
			"You are Norma planner. Create and refine a project plan conversationally. JSON formatting is not required. "+
				"If needed, run beads and shell commands directly through your ACP runtime.\n\nProject goal:\n%s", goal))
		if err := runACPPlannerTurn(cmd.Context(), adkRunner, sess.Session.ID(), initialPrompt, cmd.OutOrStdout()); err != nil {
			return err
		}
	}

	reader := bufio.NewReader(cmd.InOrStdin())
	for {
		if _, err := fmt.Fprint(cmd.OutOrStdout(), "\nplanner> "); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if strings.TrimSpace(line) == "" {
					return nil
				}
			} else {
				return err
			}
		}
		input := strings.TrimSpace(line)
		if input == "" {
			if errors.Is(err, io.EOF) {
				return nil
			}
			continue
		}
		if input == "exit" || input == "quit" {
			return nil
		}
		if turnErr := runACPPlannerTurn(cmd.Context(), adkRunner, sess.Session.ID(), input, cmd.OutOrStdout()); turnErr != nil {
			return turnErr
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func acpNoopLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func runACPPlannerTurn(ctx context.Context, r *runner.Runner, sessionID string, prompt string, out io.Writer) error {
	events := r.Run(ctx, "norma-planner-user", sessionID, genai.NewContentFromText(prompt, genai.RoleUser), adkagent.RunConfig{})
	var finalText string
	var partial strings.Builder
	for ev, err := range events {
		if err != nil {
			return fmt.Errorf("planner turn failed: %w", err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		chunk := extractEventText(ev)
		if chunk == "" {
			continue
		}
		if ev.Partial {
			partial.WriteString(chunk)
			continue
		}
		finalText = chunk
	}
	if strings.TrimSpace(finalText) == "" {
		finalText = partial.String()
	}
	if strings.TrimSpace(finalText) == "" {
		return nil
	}
	_, err := fmt.Fprintln(out, finalText)
	return err
}

func extractEventText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range ev.Content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

func allowACPPermissions(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
			}, nil
		}
	}
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
			}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}
