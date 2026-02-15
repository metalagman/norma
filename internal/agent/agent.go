// Package agent provides implementations for running different types of agents.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/metalagman/ainvoke/adk"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Runner executes an agent with a normalized request.
type Runner interface {
	Run(ctx context.Context, req models.AgentRequest, stdout, stderr io.Writer) (outBytes, errBytes []byte, exitCode int, err error)
}

// NewRunner constructs a runner for the given agent config and role.
func NewRunner(cfg config.AgentConfig, role models.Role) (Runner, error) {
	if cfg.Type == config.AgentTypeOpenAI {
		return newOpenAIRunner(cfg, role)
	}

	cmd, err := ResolveCmd(cfg)
	if err != nil {
		return nil, err
	}

	return &adkRunner{
		cfg:  cfg,
		role: role,
		cmd:  cmd,
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
		case config.AgentTypeOpenAI:
			return nil, fmt.Errorf("openai agent does not support command execution")
		default:
			return nil, fmt.Errorf("unknown agent type %q", cfg.Type)
		}
	}
	return cmd, nil
}

type adkRunner struct {
	cfg  config.AgentConfig
	role models.Role
	cmd  []string
}

func (r *adkRunner) Run(ctx context.Context, req models.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := r.role.Prompt(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("generate prompt: %w", err)
	}

	// Save prompt to logs/prompt.txt
	if req.Paths.RunDir != "" {
		promptPath := filepath.Join(req.Paths.RunDir, "logs", "prompt.txt")
		_ = os.MkdirAll(filepath.Dir(promptPath), 0o755)
		if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
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

	a, err := adk.NewExecAgent(
		req.Step.Name,
		"Norma agent",
		r.cmd,
		adk.WithExecAgentPrompt(prompt),
		adk.WithExecAgentInputSchema(r.role.InputSchema()),
		adk.WithExecAgentOutputSchema(r.role.OutputSchema()),
		adk.WithExecAgentRunDir(req.Paths.RunDir),
		adk.WithExecAgentUseTTY(r.cfg.UseTTY != nil && *r.cfg.UseTTY),
		adk.WithExecAgentStdout(stdout),
		adk.WithExecAgentStderr(stderr),
	)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create exec agent: %w", err)
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

	userContent := genai.NewContentFromText(string(inputJSON), genai.RoleUser)
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
			if stderr != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
			return nil, nil, lastExitCode, fmt.Errorf("exec agent execution error: %w", err)
		}
		if ev.Content != nil && len(ev.Content.Parts) > 0 {
			lastOutBytes = []byte(ev.Content.Parts[0].Text)
		}
	}

	if len(lastOutBytes) == 0 {
		return nil, nil, 0, fmt.Errorf("no output from exec agent")
	}

	// Parse role-specific response and map back to models.AgentResponse
	agentResp, err := r.role.MapResponse(lastOutBytes)
	if err == nil {
		// Re-marshal it to ensure consistency
		newOut, mErr := json.Marshal(agentResp)
		if mErr == nil {
			return newOut, nil, 0, nil
		}
		return lastOutBytes, nil, 0, fmt.Errorf("marshal agent response: %w", mErr)
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
