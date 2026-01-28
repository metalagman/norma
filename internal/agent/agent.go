// Package agent provides implementations for running different types of agents.
package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/metalagman/ainvoke"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/workflows/normaloop"
)

// Runner executes an agent with a normalized request.
type Runner interface {
	Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) (outBytes, errBytes []byte, exitCode int, err error)
	Describe() RunnerInfo
}

// RunnerInfo describes how an agent is invoked.
type RunnerInfo struct {
	Type     string
	Cmd      []string
	Model    string
	WorkDir  string
	RepoRoot string
	UseTTY   bool
}

// NewRunner constructs a runner for the given agent config.
func NewRunner(cfg config.AgentConfig, _ string) (Runner, error) {
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
			useTTY = true
		case "codex":
			cmd = []string{"codex", "exec"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--sandbox", "workspace-write")
			useTTY = true
		case "gemini":
			cmd = []string{"gemini"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			cmd = append(cmd, "--output-format", "text")
			useTTY = true
		case "opencode":
			cmd = []string{"opencode", "run"}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
			useTTY = true
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

func (r *ainvokeRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := normaloop.AgentPrompt(req, r.cfg.Model)
	if err != nil {
		return nil, nil, 0, err
	}

	inv := ainvoke.Invocation{
		RunDir:       req.Step.Dir,
		SystemPrompt: prompt,
		Input:        req,
		InputSchema:  normaloop.GetInputSchema(req.Step.Name),
		OutputSchema: normaloop.GetOutputSchema(req.Step.Name),
	}

	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// ainvoke handles writing input.json, validating schemas, and running the command.
	return r.runner.Run(ctx, inv, ainvoke.WithStdout(stdout), ainvoke.WithStderr(stderr))
}

func (r *ainvokeRunner) Describe() RunnerInfo {
	useTTY := false
	if r.cfg.UseTTY != nil {
		useTTY = *r.cfg.UseTTY
	}
	return RunnerInfo{
		Type:   r.cfg.Type,
		Cmd:    r.cmd,
		Model:  r.cfg.Model,
		UseTTY: useTTY,
	}
}
