// Package agent provides implementations for running different types of agents.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/ainvoke/adk"
	"github.com/metalagman/norma/internal/adk/acpagent"
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
	var agentFactory func(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) (agent.Agent, error)

	if config.IsACPType(cfg.Type) {
		cmd, err := ResolveACPCommand(cfg)
		if err != nil {
			return nil, err
		}
		agentFactory = func(ctx context.Context, req contracts.AgentRequest, _, stderr io.Writer) (agent.Agent, error) {
			workingDir := req.Paths.WorkspaceDir
			if strings.TrimSpace(workingDir) == "" {
				workingDir = req.Paths.RunDir
			}
			return acpagent.New(acpagent.Config{
				Context:              ctx,
				Name:                 "Norma" + toPascal(req.Step.Name) + "ACP",
				Description:          "Norma ACP role agent",
				Model:                cfg.Model,
				Command:              cmd,
				WorkingDir:           workingDir,
				Stderr:               stderr,
				PermissionHandler:    defaultACPPermissionHandler,
				OnSessionNewSetModel: cfg.OnSessionNewSetModel,
			})
		}
	} else {
		cmd, err := ResolveCmd(cfg)
		if err != nil {
			return nil, err
		}

		agentFactory = func(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) (agent.Agent, error) {
			prompt, err := role.Prompt(req)
			if err != nil {
				return nil, fmt.Errorf("generate prompt: %w", err)
			}

			return adk.NewExecAgent(
				req.Step.Name,
				"Norma agent",
				cmd,
				adk.WithExecAgentPrompt(prompt),
				adk.WithExecAgentInputSchema(role.InputSchema()),
				adk.WithExecAgentOutputSchema(role.OutputSchema()),
				adk.WithExecAgentRunDir(req.Paths.RunDir),
				adk.WithExecAgentUseTTY(cfg.UseTTY != nil && *cfg.UseTTY),
				adk.WithExecAgentStdout(stdout),
				adk.WithExecAgentStderr(stderr),
			)
		}
	}

	return &adkRunner{
		cfg:          cfg,
		role:         role,
		agentFactory: agentFactory,
	}, nil
}

// ResolveCmd resolves the command for an agent config.
func ResolveCmd(cfg config.AgentConfig) ([]string, error) {
	cmd := cfg.Cmd
	if len(cmd) == 0 {
		switch cfg.Type {
		case config.AgentTypeExec:
			return nil, fmt.Errorf("exec agent requires cmd")
		case config.AgentTypeClaude:
			cmd = []string{"claude"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
		case config.AgentTypeCodex:
			cmd = []string{"codex", "exec"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--sandbox", "workspace-write")
		case config.AgentTypeGemini:
			cmd = []string{"gemini"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--approval-mode", "yolo")
		case config.AgentTypeOpenCode:
			cmd = []string{"opencode", "run"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
		default:
			return nil, fmt.Errorf("unknown agent type %q", cfg.Type)
		}
	}
	return cmd, nil
}

// ResolveACPCommand resolves the command for ACP-backed agent types.
func ResolveACPCommand(cfg config.AgentConfig) ([]string, error) {
	switch cfg.Type {
	case config.AgentTypeACPExec:
		if len(cfg.Cmd) == 0 {
			return nil, fmt.Errorf("acp_exec agent requires cmd")
		}
		cmd := append([]string(nil), cfg.Cmd...)
		return append(cmd, cfg.ExtraArgs...), nil
	case config.AgentTypeGeminiACP:
		cmd := []string{"gemini", "--experimental-acp"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		return append(cmd, cfg.ExtraArgs...), nil
	case config.AgentTypeOpenCodeACP:
		cmd := []string{"opencode", "acp"}
		return append(cmd, cfg.ExtraArgs...), nil
	case config.AgentTypeCodexACP:
		exePath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
		cmd := []string{exePath, "proxy", "codex-acp"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		if len(cfg.ExtraArgs) > 0 {
			cmd = append(cmd, "--")
			cmd = append(cmd, cfg.ExtraArgs...)
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown acp agent type %q", cfg.Type)
	}
}

type adkRunner struct {
	cfg          config.AgentConfig
	role         contracts.Role
	agentFactory func(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) (agent.Agent, error)
}

func (r *adkRunner) Run(ctx context.Context, req contracts.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := r.role.Prompt(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("generate prompt: %w", err)
	}

	// Save prompt to logs/prompt.txt
	if req.Paths.RunDir != "" {
		promptPath := filepath.Join(req.Paths.RunDir, "logs", "prompt.txt")
		_ = os.MkdirAll(filepath.Dir(promptPath), 0o700)
		if err := os.WriteFile(promptPath, []byte(prompt), 0o600); err != nil {
			log.Warn().Err(err).Str("path", promptPath).Msg("failed to save prompt log")
		}
	}

	input, err := r.role.MapRequest(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("map request: %w", err)
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshal input: %w", err)
	}

	a, err := r.agentFactory(ctx, req, stdout, stderr)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create agent: %w", err)
	}
	if closer, ok := a.(interface{ Close() error }); ok {
		defer func() {
			if closeErr := closer.Close(); closeErr != nil {
				log.Warn().Err(closeErr).Msg("failed to close agent runtime")
			}
		}()
	}

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

	userPayload := string(inputJSON)
	if config.IsACPType(r.cfg.Type) {
		userPayload = buildACPPayload(prompt, inputJSON, r.role)
	}
	userContent := genai.NewContentFromText(userPayload, genai.RoleUser)
	events := adkRunner.Run(ctx, userID, sess.Session.ID(), userContent, agent.RunConfig{})

	var lastOutBytes []byte
	var lastExitCode int
	for ev, err := range events {
		if err != nil {
			// Extract exit code if possible
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

	// Parse role-specific response and map back to normalized AgentResponse.
	agentResp, err := r.role.MapResponse(lastOutBytes)
	if err == nil {
		// Re-marshal it to ensure consistency
		newOut, mErr := json.Marshal(agentResp)
		if mErr == nil {
			return newOut, nil, 0, nil
		}
		return lastOutBytes, nil, 0, fmt.Errorf("marshal agent response: %w", mErr)
	}

	// Try extracting JSON if direct mapping failed
	if extracted, ok := ExtractJSON(lastOutBytes); ok {
		agentResp, err = r.role.MapResponse(extracted)
		if err == nil {
			newOut, mErr := json.Marshal(agentResp)
			if mErr == nil {
				return newOut, nil, 0, nil
			}
			return extracted, nil, 0, fmt.Errorf("marshal agent response: %w", mErr)
		}
	}

	return lastOutBytes, nil, 0, fmt.Errorf("parse agent response: %w", err)
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

func buildACPPayload(prompt string, inputJSON []byte, role contracts.Role) string {
	const outputRule = `Return only valid JSON for the final AgentResponse object. Do not wrap in markdown.`
	return strings.TrimSpace(strings.Join([]string{
		prompt,
		outputRule,
		"Role: " + role.Name(),
		"Input JSON:",
		string(inputJSON),
	}, "\n\n"))
}

func toPascal(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
