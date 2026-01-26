// Package agent provides implementations for running different types of agents.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creack/pty"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/rs/zerolog/log"
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
func NewRunner(cfg config.AgentConfig, repoRoot string) (Runner, error) {
	switch cfg.Type {
	case "exec":
		if len(cfg.Cmd) == 0 {
			return nil, fmt.Errorf("exec agent requires cmd")
		}
		return &execRunner{repoRoot: repoRoot, cmd: cfg.Cmd, workDir: repoRoot}, nil
	case "codex":
		if len(cfg.Cmd) > 0 {
			return nil, fmt.Errorf("codex agent does not support cmd configuration")
		}
		workDir := repoRoot
		if cfg.Path != "" {
			if filepath.IsAbs(cfg.Path) {
				workDir = cfg.Path
			} else {
				workDir = filepath.Join(repoRoot, cfg.Path)
			}
		}
		useTTY := false
		if cfg.UseTTY != nil {
			useTTY = *cfg.UseTTY
		}
		return &codexRunner{repoRoot: repoRoot, cmd: []string{"codex"}, model: cfg.Model, workDir: workDir, useTTY: useTTY}, nil
	case "opencode":
		if len(cfg.Cmd) > 0 {
			return nil, fmt.Errorf("opencode agent does not support cmd configuration")
		}
		workDir := repoRoot
		if cfg.Path != "" {
			if filepath.IsAbs(cfg.Path) {
				workDir = cfg.Path
			} else {
				workDir = filepath.Join(repoRoot, cfg.Path)
			}
		}
		useTTY := false
		if cfg.UseTTY != nil {
			useTTY = *cfg.UseTTY
		}
		return &opencodeRunner{repoRoot: repoRoot, cmd: []string{"opencode"}, model: cfg.Model, workDir: workDir, useTTY: useTTY}, nil
	case "gemini":
		if len(cfg.Cmd) > 0 {
			return nil, fmt.Errorf("gemini agent does not support cmd configuration")
		}
		workDir := repoRoot
		if cfg.Path != "" {
			if filepath.IsAbs(cfg.Path) {
				workDir = cfg.Path
			} else {
				workDir = filepath.Join(repoRoot, cfg.Path)
			}
		}
		useTTY := false
		if cfg.UseTTY != nil {
			useTTY = *cfg.UseTTY
		}
		return &geminiRunner{repoRoot: repoRoot, cmd: []string{"gemini"}, model: cfg.Model, workDir: workDir, useTTY: useTTY}, nil
	case "claude":
		if len(cfg.Cmd) > 0 {
			return nil, fmt.Errorf("claude agent does not support cmd configuration")
		}
		workDir := repoRoot
		if cfg.Path != "" {
			if filepath.IsAbs(cfg.Path) {
				workDir = cfg.Path
			} else {
				workDir = filepath.Join(repoRoot, cfg.Path)
			}
		}
		useTTY := false
		if cfg.UseTTY != nil {
			useTTY = *cfg.UseTTY
		}
		return &claudeRunner{repoRoot: repoRoot, cmd: []string{"claude"}, model: cfg.Model, workDir: workDir, useTTY: useTTY}, nil
	default:
		return nil, fmt.Errorf("unknown agent type %q", cfg.Type)
	}
}

type execRunner struct {
	repoRoot string
	cmd      []string
	workDir  string
}

func (r *execRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshal request: %w", err)
	}
	return runCommand(ctx, r.cmd, r.effectiveWorkDir(req), data, stdout, stderr)
}

func (r *execRunner) Describe() RunnerInfo {
	return RunnerInfo{Type: "exec", Cmd: r.cmd, WorkDir: r.workDir, RepoRoot: r.repoRoot}
}

func (r *execRunner) effectiveWorkDir(req model.AgentRequest) string {
	return req.Paths.RunDir
}

type codexRunner struct {
	repoRoot string
	cmd      []string
	model    string
	workDir  string
	useTTY   bool
}

func (r *codexRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := agentPrompt(req, r.model)
	if err != nil {
		return nil, nil, 0, err
	}
	argv := appendCodexFlags(r.cmd, r.model)
	workDir := r.effectiveWorkDir(req)
	if r.useTTY {
		log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", true).Msg("run codex agent")
		return runCommandWithTTY(ctx, argv, workDir, []byte(prompt), stdout)
	}
	log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", false).Msg("run codex agent")
	return runCommand(ctx, argv, workDir, []byte(prompt), stdout, stderr)
}

func (r *codexRunner) Describe() RunnerInfo {
	return RunnerInfo{Type: "codex", Cmd: r.cmd, Model: r.model, WorkDir: r.workDir, RepoRoot: r.repoRoot, UseTTY: r.useTTY}
}

func (r *codexRunner) effectiveWorkDir(req model.AgentRequest) string {
	return req.Paths.RunDir
}

type opencodeRunner struct {
	repoRoot string
	cmd      []string
	model    string
	workDir  string
	useTTY   bool
}

func (r *opencodeRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := agentPrompt(req, r.model)
	if err != nil {
		return nil, nil, 0, err
	}
	argv := appendOpenCodeFlags(r.cmd, r.model)
	argv = append(argv, prompt)
	workDir := r.effectiveWorkDir(req)
	if r.useTTY {
		log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", true).Msg("run opencode agent")
		return runCommandWithTTY(ctx, argv, workDir, nil, stdout)
	}
	log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", false).Msg("run opencode agent")
	return runCommand(ctx, argv, workDir, nil, stdout, stderr)
}

func (r *opencodeRunner) Describe() RunnerInfo {
	return RunnerInfo{Type: "opencode", Cmd: r.cmd, Model: r.model, WorkDir: r.workDir, RepoRoot: r.repoRoot, UseTTY: r.useTTY}
}

func (r *opencodeRunner) effectiveWorkDir(req model.AgentRequest) string {
	return req.Paths.RunDir
}

type geminiRunner struct {
	repoRoot string
	cmd      []string
	model    string
	workDir  string
	useTTY   bool
}

func (r *geminiRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := agentPrompt(req, r.model)
	if err != nil {
		return nil, nil, 0, err
	}
	argv := appendGeminiFlags(r.cmd, r.model)
	argv = append(argv, prompt)
	workDir := r.effectiveWorkDir(req)
	if r.useTTY {
		log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", true).Msg("run gemini agent")
		return runCommandWithTTY(ctx, argv, workDir, nil, stdout)
	}
	log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", false).Msg("run gemini agent")
	return runCommand(ctx, argv, workDir, nil, stdout, stderr)
}

func (r *geminiRunner) Describe() RunnerInfo {
	return RunnerInfo{Type: "gemini", Cmd: r.cmd, Model: r.model, WorkDir: r.workDir, RepoRoot: r.repoRoot, UseTTY: r.useTTY}
}

func (r *geminiRunner) effectiveWorkDir(req model.AgentRequest) string {
	return req.Paths.RunDir
}

type claudeRunner struct {
	repoRoot string
	cmd      []string
	model    string
	workDir  string
	useTTY   bool
}

func (r *claudeRunner) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := agentPrompt(req, r.model)
	if err != nil {
		return nil, nil, 0, err
	}
	argv := appendClaudeFlags(r.cmd, r.model)
	argv = append(argv, prompt)
	workDir := r.effectiveWorkDir(req)
	if r.useTTY {
		log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", true).Msg("run claude agent")
		return runCommandWithTTY(ctx, argv, workDir, nil, stdout)
	}
	log.Debug().Strs("cmd", argv).Str("work_dir", workDir).Bool("tty", false).Msg("run claude agent")
	return runCommand(ctx, argv, workDir, nil, stdout, stderr)
}

func (r *claudeRunner) Describe() RunnerInfo {
	return RunnerInfo{Type: "claude", Cmd: r.cmd, Model: r.model, WorkDir: r.workDir, RepoRoot: r.repoRoot, UseTTY: r.useTTY}
}

func (r *claudeRunner) effectiveWorkDir(req model.AgentRequest) string {
	return req.Paths.RunDir
}

func runCommand(ctx context.Context, argv []string, workDir string, stdin []byte, stdoutSink, stderrSink io.Writer) ([]byte, []byte, int, error) {
	if len(argv) == 0 {
		return nil, nil, 0, fmt.Errorf("agent command is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if stdoutSink != nil {
		cmd.Stdout = io.MultiWriter(&stdout, stdoutSink)
	} else {
		cmd.Stdout = &stdout
	}
	if stderrSink != nil {
		cmd.Stderr = io.MultiWriter(&stderr, stderrSink)
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), err
		}
		return stdout.Bytes(), stderr.Bytes(), 0, err
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

func runCommandWithTTY(ctx context.Context, argv []string, workDir string, stdin []byte, stdoutSink io.Writer) ([]byte, []byte, int, error) {
	if len(argv) == 0 {
		return nil, nil, 0, fmt.Errorf("agent command is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workDir
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("start pty: %w", err)
	}
	var out bytes.Buffer
	var outWriter io.Writer = &out
	if stdoutSink != nil {
		outWriter = io.MultiWriter(&out, stdoutSink)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(outWriter, ptmx)
		done <- err
	}()
	if len(stdin) > 0 {
		if _, err := ptmx.Write(stdin); err != nil {
			_ = ptmx.Close()
			_ = cmd.Wait()
			return out.Bytes(), nil, 0, fmt.Errorf("write stdin: %w", err)
		}
	}
	_, _ = ptmx.Write([]byte{4})
	err = cmd.Wait()
	_ = ptmx.Close()
	<-done
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return out.Bytes(), nil, exitErr.ExitCode(), err
		}
		return out.Bytes(), nil, 0, err
	}
	return out.Bytes(), nil, 0, nil
}

func appendCodexFlags(argv []string, model string) []string {
	out := make([]string, 0, len(argv)+4)
	out = append(out, argv...)
	if len(out) > 0 && out[0] == "codex" {
		if len(out) == 1 || !isCodexSubcommand(out[1]) {
			out = append(out[:1], append([]string{"exec"}, out[1:]...)...)
		}
	}
	if model != "" && !hasFlag(out, "--model") && !hasFlag(out, "-m") {
		out = append(out, "--model", model)
	}
	if !hasFlag(out, "--full-auto") {
		out = append(out, "--full-auto")
	}
	if !hasFlag(out, "--skip-git-repo-check") {
		out = append(out, "--skip-git-repo-check")
	}

	return out
}

func appendOpenCodeFlags(argv []string, model string) []string {
	out := make([]string, 0, len(argv)+4)
	out = append(out, argv...)
	if len(out) > 0 && out[0] == "opencode" {
		if len(out) == 1 || out[1] == "" || strings.HasPrefix(out[1], "-") || !isOpenCodeSubcommand(out[1]) {
			out = append(out[:1], append([]string{"run"}, out[1:]...)...)
		}
	}
	if model != "" && !hasFlag(out, "--model") && !hasFlag(out, "-m") {
		out = append(out, "--model", model)
	}
	return out
}

func appendGeminiFlags(argv []string, model string) []string {
	out := make([]string, 0, len(argv)+4)
	out = append(out, argv...)
	if model != "" && !hasFlag(out, "--model") && !hasFlag(out, "-m") {
		out = append(out, "--model", model)
	}
	if !hasFlag(out, "--output-format") {
		out = append(out, "--output-format", "text")
	}
	if !hasFlag(out, "--approval-mode") && !hasFlag(out, "--yolo") {
		out = append(out, "--approval-mode", "yolo")
	}
	return out
}

func appendClaudeFlags(argv []string, model string) []string {
	out := make([]string, 0, len(argv)+4)
	out = append(out, argv...)
	if model != "" && !hasFlag(out, "--model") && !hasFlag(out, "-m") {
		out = append(out, "--model", model)
	}
	if !hasFlag(out, "--output-format") {
		out = append(out, "--output-format", "text")
	}
	if !hasFlag(out, "--print") && !hasFlag(out, "-p") {
		out = append(out, "--print")
	}
	if !hasFlag(out, "--dangerously-skip-permissions") {
		out = append(out, "--dangerously-skip-permissions")
	}
	return out
}

func hasFlag(argv []string, name string) bool {
	for _, arg := range argv {
		if arg == name {
			return true
		}
	}
	return false
}

func isCodexSubcommand(arg string) bool {
	switch arg {
	case "exec", "review", "login", "logout", "mcp", "mcp-server", "app-server",
		"completion", "sandbox", "apply", "resume", "fork", "cloud", "features", "help":
		return true
	default:
		return false
	}
}

func isOpenCodeSubcommand(arg string) bool {
	switch arg {
	case "agent", "attach", "auth", "github", "mcp", "models", "run", "serve",
		"session", "stats", "export", "import", "web", "acp", "uninstall", "upgrade", "help":
		return true
	default:
		return false
	}
}

func agentPrompt(req model.AgentRequest, modelName string) (string, error) {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	var b strings.Builder
	b.WriteString("You are a norma agent. Follow the instructions strictly.\n")
	b.WriteString("- You are running in the 'run_dir', which is the parent of both the isolated code workspace and your step directory.\n")
	b.WriteString("- Use 'paths.workspace_dir' as the root for all code reading and writing tasks.\n")
	b.WriteString("- A full history of this run is available in 'context.journal' and reconstructed in 'artifacts/progress.md'. Use it to understand previous attempts and avoid repeating mistakes.\n")
	b.WriteString("- Write your AgentResponse JSON and all logs/evidence directly into your step directory: '")
	b.WriteString(req.Step.Dir)
	b.WriteString("'.\n")
	b.WriteString("- Output ONLY valid JSON for AgentResponse on stdout.\n")
	b.WriteString("- Follow the norma-loop: plan -> do -> check -> act.\n")
	b.WriteString("- Workspace exists before any agent runs.\n")
	b.WriteString("- Agents never modify workspace or git directly (except for Do and Act).\n")
	b.WriteString("- All agents operate in read-only mode with respect to workspace/ (except Do and Act).\n")
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
	case roleDo:
		b.WriteString("Role requirements: execute only plan.work_plan.do_steps[*] and record what was executed.\n")
	case roleCheck:
		b.WriteString("Role requirements: verify plan match (planned vs executed), verify job done (all effective ACs evaluated), and emit a verdict in the 'check' field of the JSON output.\n")
	case roleAct:
		b.WriteString("Role requirements: consume Check verdict and decide what to do next.\n")
	}
	b.WriteString("\nAgentRequest:\n")
	b.Write(data)
	b.WriteString("\n")
	return b.String(), nil
}
