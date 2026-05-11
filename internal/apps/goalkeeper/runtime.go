package goalkeeper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultAgentType = "codex_acp"
	defaultModel     = "gpt-5.3-codex"

	rootAgentName      = "Goalkeeper"
	workerAgentID      = "worker"
	validatorAgentID   = "validator"
	workerAgentName    = "GoalkeeperWorker"
	validatorAgentName = "GoalkeeperValidator"

	appName   = "goalkeeper"
	userID    = "goalkeeper-user"
	sessionID = "goalkeeper-session"
)

// Config configures the Goalkeeper playground run.
type Config struct {
	Goal       string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

type closableAgent interface {
	agent.Agent
	Close() error
}

type acpRuntimeConfig struct {
	AgentID     string
	Name        string
	Description string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
}

type runtimeDeps struct {
	newACPAgent func(context.Context, acpRuntimeConfig) (closableAgent, error)
	runAgent    func(context.Context, agent.Agent, string, func(string)) (string, error)
}

// Run executes the Goalkeeper playground workflow once.
func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, defaultDeps())
}

func defaultDeps() runtimeDeps {
	return runtimeDeps{
		newACPAgent: newACPAgent,
		runAgent:    runWorkflowAgent,
	}
}

func run(ctx context.Context, cfg Config, deps runtimeDeps) error {
	startedAt := time.Now()
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		workingDir = "."
	}

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdout = &syncWriter{writer: stdout}

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stderr = &syncWriter{writer: stderr}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		baseLogger = zerolog.Ctx(ctx)
	}
	logger := baseLogger.With().
		Str("component", "playground.goalkeeper").
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	worker, err := deps.newACPAgent(ctx, acpRuntimeConfig{
		AgentID:     workerAgentID,
		Name:        workerAgentName,
		Description: "Goalkeeper worker agent",
		Instruction: workerInstruction(),
		Command:     command,
		WorkingDir:  workingDir,
		Stderr:      stderr,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	defer closeAgent(logger, worker)

	validator, err := deps.newACPAgent(ctx, acpRuntimeConfig{
		AgentID:     validatorAgentID,
		Name:        validatorAgentName,
		Description: "Goalkeeper validator agent",
		Instruction: validatorInstruction(),
		Command:     command,
		WorkingDir:  workingDir,
		Stderr:      stderr,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	defer closeAgent(logger, validator)

	workflow, err := newWorkflowAgent(worker, validator)
	if err != nil {
		return err
	}

	prompt := formatGoalPrompt(goal)
	logger.Info().
		Str("working_dir", workingDir).
		Strs("command", command).
		Int("goal_len", len(goal)).
		Msg("goalkeeper started")
	result, err := deps.runAgent(ctx, workflow, prompt, func(output string) {
		logger.Debug().Str("output", strings.TrimSpace(output)).Msg("goalkeeper workflow output")
	})
	if err != nil {
		return err
	}
	elapsed := formatElapsed(time.Since(startedAt))
	logger.Info().
		Bool("has_result", strings.TrimSpace(result) != "").
		Str("elapsed", elapsed).
		Msg("goalkeeper completed")
	return writeRunOutput(stdout, strings.TrimSpace(result), elapsed)
}

func newACPAgent(ctx context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
	logger := cfg.Logger.With().Str("agent_id", cfg.AgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              cfg.Name,
		Description:       cfg.Description,
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       cfg.Instruction,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}
	return agentRuntime, nil
}

func newWorkflowAgent(worker, validator agent.Agent) (agent.Agent, error) {
	return sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        rootAgentName,
			Description: "Runs a worker agent and then a validator agent for one goal.",
			SubAgents:   []agent.Agent{worker, validator},
		},
	})
}

func runWorkflowAgent(ctx context.Context, a agent.Agent, prompt string, onOutput func(string)) (string, error) {
	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		return "", fmt.Errorf("create goalkeeper runner: %w", err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("create goalkeeper session: %w", err)
	}

	return runWithRunner(ctx, runner, userID, created.Session.ID(), prompt, onOutput)
}

func runWithRunner(
	ctx context.Context,
	runner *adkrunner.Runner,
	userID string,
	sessionID string,
	prompt string,
	onOutput func(string),
) (string, error) {
	var lastContent *genai.Content
	events := runner.Run(ctx, userID, sessionID, genai.NewContentFromText(prompt, genai.RoleUser), agent.RunConfig{})
	for ev, runErr := range events {
		if runErr != nil {
			return "", runErr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		lastContent = ev.Content
		output := contentText(ev.Content)
		if onOutput != nil && output != "" {
			onOutput(output)
		}
	}
	return contentText(lastContent), nil
}

func workerInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper worker agent.",
		"You receive one user goal as plain text.",
		"Do the requested work in the current working directory.",
		"Return a concise plain-text summary of what you did and the evidence the validator should inspect.",
		"Do not validate the final result beyond immediate sanity checks.",
	}, "\n")
}

func validatorInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper validator agent.",
		"Validate the prior worker result against the original user goal using the shared ADK session context.",
		"Inspect the current working directory as needed.",
		"Do not intentionally mutate files or continue the worker's implementation work.",
		"Start with exactly `verdict: pass` or `verdict: fail`.",
		"Then provide brief evidence and a concise final summary.",
	}, "\n")
}

func formatGoalPrompt(goal string) string {
	return "Goal:\n" + strings.TrimSpace(goal)
}

func formatElapsed(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func writeRunOutput(stdout io.Writer, summary string, elapsed string) error {
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		if _, err := fmt.Fprintln(stdout, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "Total run time: %s\n", elapsed)
	return err
}

// BuildCodexACPCommand builds the command used to launch the Codex ACP bridge.
func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func closeAgent(logger zerolog.Logger, a closableAgent) {
	if a == nil {
		return
	}
	if err := a.Close(); err != nil {
		logger.Warn().Err(err).Str("agent", a.Name()).Msg("failed to close goalkeeper agent")
	}
}

func autoAllowPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
