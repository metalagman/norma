// Package pdca provides the PDCA workflow runner.
package pdca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/agentfactory"
	"github.com/metalagman/norma/internal/adk/structured"
	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/config"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Runner executes an agent with a normalized request.
type Runner interface {
	Run(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) (outBytes, errBytes []byte, exitCode int, err error)
}

// NewRunner constructs a runner for the given agent config and role.
func NewRunner(cfg config.AgentConfig, role contracts.Role) (Runner, error) {
	return &adkRunner{
		cfg:  cfg,
		role: role,
	}, nil
}

type adkRunner struct {
	cfg  config.AgentConfig
	role contracts.Role
}

func (r *adkRunner) Run(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	l := log.With().Str("role", r.role.Name()).Logger()

	// 1. Map request to JSON input for the role.
	input, err := r.role.MapRequest(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("map request: %w", err)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshal input JSON: %w", err)
	}

	// 2. Resolve system instruction (role-specific prompt).
	systemInstruction, err := r.role.Prompt(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("generate role prompt: %w", err)
	}

	// 3. Resolve working directory.
	workingDirectory := strings.TrimSpace(req.Paths.WorkspaceDir)
	if workingDirectory == "" {
		workingDirectory = strings.TrimSpace(req.Paths.RunDir)
	}

	// 4. Create ephemeral inner agent via factory.
	factory := agentfactory.NewFactory(map[string]config.AgentConfig{
		r.role.Name(): r.cfg,
	})
	creationReq := agentfactory.CreationRequest{
		Name:              "Norma" + toPascal(req.Step.Name) + "Agent",
		Description:       "Norma " + req.Step.Name + " agent",
		SystemInstruction: systemInstruction,
		WorkingDirectory:  workingDirectory,
		Stdout:            stdout,
		Stderr:            stderr,
		PermissionHandler: defaultACPPermissionHandler,
	}

	inner, err := factory.CreateAgent(ctx, r.role.Name(), creationReq)
	if err != nil {
		return nil, nil, 1, fmt.Errorf("failed to create inner agent: %w", err)
	}
	if closer, ok := inner.(interface{ Close() error }); ok {
		defer func() {
			if closeErr := closer.Close(); closeErr != nil {
				l.Warn().Err(closeErr).Msg("failed to close inner agent runtime")
			}
		}()
	}

	// 5. Wrap with structured I/O agent.
	a, err := structured.NewAgent(inner,
		structured.WithInputSchema(r.role.InputSchema()),
		structured.WithOutputSchema(r.role.OutputSchema()),
	)
	if err != nil {
		return nil, nil, 1, fmt.Errorf("failed to create structured wrapper: %w", err)
	}

	// 6. Execute via ADK runner.
	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create adk runner: %w", err)
	}

	userID := "norma-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  userID,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create session: %w", err)
	}

	userContent := genai.NewContentFromText(string(inputJSON), genai.RoleUser)
	events := adkRunner.Run(ctx, userID, sess.Session.ID(), userContent, agent.RunConfig{})

	var lastOutBytes []byte
	var lastExitCode int
	for ev, err := range events {
		if err != nil {
			if exitErr, ok := err.(interface{ ExitCode() int }); ok {
				lastExitCode = exitErr.ExitCode()
			} else {
				lastExitCode = 1
			}
			return nil, nil, lastExitCode, fmt.Errorf("agent execution error: %w", err)
		}
		if ev.Content != nil && len(ev.Content.Parts) > 0 {
			lastOutBytes = []byte(ev.Content.Parts[0].Text)
		}
	}

	if len(lastOutBytes) == 0 {
		return nil, nil, 0, fmt.Errorf("no output from agent")
	}

	// 7. Extract and map final response.
	extracted, ok := ExtractJSON(lastOutBytes)
	if !ok {
		extracted = lastOutBytes
	}

	// Validate that it actually matches the role response (mapped via role.MapResponse).
	agentResp, err := r.role.MapResponse(extracted)
	if err != nil {
		return extracted, nil, 0, fmt.Errorf("map agent response: %w", err)
	}

	// Final normalization to ensure it is clean JSON.
	normalized, err := json.Marshal(agentResp)
	if err != nil {
		return extracted, nil, 0, fmt.Errorf("marshal normalized response: %w", err)
	}

	return normalized, nil, 0, nil
}

func toPascal(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ExtractJSON finds the first JSON object in a byte slice.
func ExtractJSON(data []byte) ([]byte, bool) {
	start := -1
	for i, b := range data {
		if b == '{' {
			start = i
			break
		}
	}
	end := -1
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '}' {
			end = i
			break
		}
	}
	if start == -1 || end == -1 || start >= end {
		return nil, false
	}
	return data[start : end+1], true
}

func defaultACPPermissionHandler(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
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
