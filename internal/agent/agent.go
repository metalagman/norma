// Package agent provides implementations for running different types of agents.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/metalagman/ainvoke"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop"
)

// Runner executes an agent with a normalized request.
type Runner interface {
	Run(ctx context.Context, req normaloop.AgentRequest, stdout, stderr io.Writer) (outBytes, errBytes []byte, exitCode int, err error)
}

// NewRunner constructs a runner for the given agent config.
func NewRunner(cfg config.AgentConfig) (Runner, error) {
	cmd := cfg.Cmd
	useTTY := false

	if len(cmd) == 0 {
		switch cfg.Type {
		case "exec":
			return nil, fmt.Errorf("exec agent requires cmd")
		case "claude":
			cmd = []string{"claude"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
		case "codex":
			// Keep non-TTY so codex reads the prompt from stdin.
			cmd = []string{"codex", "exec"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--sandbox", "workspace-write")
		case "gemini":
			cmd = []string{"gemini"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--output-format", "text")
			// Force one-shot mode (non-interactive) and allow file writes without prompts.
			cmd = append(cmd, "--prompt", "", "--approval-mode", "auto_edit")
		case "opencode":
			cmd = []string{"opencode", "run"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
		default:
			return nil, fmt.Errorf("unknown agent type %q", cfg.Type)
		}
	}

	if cfg.UseTTY != nil {
		useTTY = *cfg.UseTTY
	}

	ar, err := ainvoke.NewRunner(ainvoke.AgentConfig{
		Cmd:    cmd,
		UseTTY: useTTY,
	})
	if err != nil {
		return nil, err
	}

	return &ainvokeRunner{
		cfg:    cfg,
		runner: ar,
		cmd:    cmd,
	}, nil
}

type ainvokeRunner struct {
	cfg    config.AgentConfig
	runner ainvoke.Runner
	cmd    []string
}

func (r *ainvokeRunner) Run(ctx context.Context, req normaloop.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	role := normaloop.GetRole(req.Step.Name)
	if role == nil {
		return nil, nil, 0, fmt.Errorf("unknown role %q", req.Step.Name)
	}

	prompt, err := role.Prompt(req)
	if err != nil {
		return nil, nil, 0, err
	}

	input, err := role.MapRequest(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("map request: %w", err)
	}

	inv := ainvoke.Invocation{
		RunDir:       req.Paths.RunDir,
		SystemPrompt: prompt,
		Input:        input,
		InputSchema:  role.InputSchema(),
		OutputSchema: role.OutputSchema(),
	}

	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// ainvoke handles writing input.json, validating schemas, and running the command.
	outBytes, errBytes, exitCode, err := r.runner.Run(ctx, inv, ainvoke.WithStdout(stdout), ainvoke.WithStderr(stderr))
	if err != nil {
		writeInvocationError(stderr, err)
		return outBytes, errBytes, exitCode, err
	}

	// Parse role-specific response and map back to normaloop.AgentResponse
	agentResp, err := role.MapResponse(outBytes)
	if err == nil {
		// Re-marshal it to ensure consistency
		newOut, mErr := json.Marshal(agentResp)
		if mErr == nil {
			return newOut, errBytes, exitCode, nil
		}
	}

	return outBytes, errBytes, exitCode, nil
}

func writeInvocationError(w io.Writer, err error) {
	if w == nil || err == nil {
		return
	}
	_, _ = fmt.Fprintln(w, err)
}
