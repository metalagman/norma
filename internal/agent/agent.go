// Package agent provides implementations for running different types of agents.
package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/metalagman/ainvoke"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
)

const (
	rolePlan  = "plan"
	roleDo    = "do"
	roleCheck = "check"
	roleAct   = "act"
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
	if len(cmd) == 0 {
		switch cfg.Type {
		case "exec":
			return nil, fmt.Errorf("exec agent requires cmd")
		case "claude", "codex", "gemini", "opencode":
			cmd = []string{"ainvoke", cfg.Type}
			if cfg.Model != "" {
				cmd = append(cmd, "--model", cfg.Model)
			}
		default:
			return nil, fmt.Errorf("unknown agent type %q", cfg.Type)
		}
	}

	useTTY := false
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
	prompt, err := agentPrompt(req, r.cfg.Model)
	if err != nil {
		return nil, nil, 0, err
	}

	inv := ainvoke.Invocation{
		RunDir:       req.Step.Dir,
		SystemPrompt: prompt,
		Input:        req,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
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

func agentPrompt(req model.AgentRequest, modelName string) (string, error) {
	var b strings.Builder
	b.WriteString("You are a norma agent. Follow the instructions strictly.\n")
	b.WriteString("- You are running in your step directory.\n")
	b.WriteString("- Use 'paths.workspace_dir' as the root for all code reading and writing tasks.\n")
	b.WriteString("- IMPORTANT: Do NOT attempt to read or index the entire codebase. Only examine files relevant to the current task.\n")
	b.WriteString("- IMPORTANT: Do NOT use recursive listing tools (like 'ls -R', 'find', or 'grep -r') on the root directory. Explore the codebase incrementally and specifically.\n")
	b.WriteString("- A full history of this run is available in 'context.journal' and reconstructed in 'artifacts/progress.md'. Use it to understand previous attempts and avoid repeating mistakes.\n")
	b.WriteString("- Follow the norma-loop: plan -> do -> check -> act.\n")
	b.WriteString("- Workspace exists before any agent runs.\n")
	b.WriteString("- Agents never modify workspace or git directly (except for Do and Act).\n")
	b.WriteString("- Agents never modify task state, labels, or metadata directly; this is handled by the orchestrator.\n")
	b.WriteString("- All agents operate in read-only mode with respect to the codebase (except Do and Act).\n")
	b.WriteString("- IMPORTANT: In 'do' and 'act' steps, you MUST commit your changes in the 'workspace_dir' using 'git add' and 'git commit'.\n")
	b.WriteString("- IMPORTANT: Do NOT scan or index the entire 'run_dir'. Focus only on the 'workspace_dir' for code context.\n")
	b.WriteString("- Use status='ok' if you successfully completed your task, even if tests failed or results are not perfect.\n")
	b.WriteString("- Use status='stop' or 'error' only for technical failures or when budgets are exceeded.\n")

	if modelName != "" {
		b.WriteString("- Use model hint: ")
		b.WriteString(modelName)
		b.WriteString(" (if relevant).\n")
	}

	switch req.Step.Name {
	case rolePlan:
		b.WriteString("Role requirements: produce work_plan and publish acceptance_criteria.effective.\n")
		b.WriteString("- Focus on creating a clear, actionable plan for the immediate iteration. Think about HOW to achieve the goal through code changes.\n")
		b.WriteString("- Limit observations and research to what is strictly necessary for planning value. Avoid making a lot of observations without producing actual changes in the subsequent 'do' step.\n")
		b.WriteString("- Keep the work_plan focused and small.\n")
	case roleDo:
		b.WriteString("Role requirements: execute only plan.work_plan.do_steps[*] and record what was executed.\n")
	case roleCheck:
		b.WriteString("Role requirements: verify plan match (planned vs executed), verify job done (all effective ACs evaluated), and emit a verdict in the 'check' field of the JSON output.\n")
	case roleAct:
		b.WriteString("Role requirements: consume Check verdict and decide what to do next.\n")
	}

	return b.String(), nil
}
