package goalkeeper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	goalkeeperworkflow "github.com/normahq/norma/pkg/goalkeeper"
	"github.com/normahq/norma/pkg/runtime/acpagent"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultAgentType     = "codex_acp"
	defaultModel         = "gpt-5.3-codex"
	defaultMaxIterations = uint(5)

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
	Goal          string
	WorkingDir    string
	BridgeBin     string
	MaxIterations uint
	Stdout        io.Writer
	Stderr        io.Writer
	Logger        *zerolog.Logger
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

type stepLogSpec struct {
	Index int
	ID    string
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
	maxIterations := cfg.MaxIterations
	if maxIterations == 0 {
		maxIterations = defaultMaxIterations
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

	workflow, err := newWorkflowAgent(worker, validator, maxIterations, logger)
	if err != nil {
		return err
	}

	prompt := formatGoalPrompt(goal)
	logger.Info().
		Str("working_dir", workingDir).
		Strs("command", command).
		Int("goal_len", len(goal)).
		Uint("max_iterations", maxIterations).
		Msg("goalkeeper started")
	result, err := deps.runAgent(ctx, workflow, prompt, nil)
	if err != nil {
		return err
	}
	elapsed := formatElapsed(time.Since(startedAt))
	verdict, goalReached := parseVerdict(result)
	logger.Info().
		Str("verdict", verdict).
		Bool("goal_reached", goalReached).
		Msg("goalkeeper validation completed")
	logger.Info().
		Bool("has_result", strings.TrimSpace(result) != "").
		Str("elapsed", elapsed).
		Msg("goalkeeper completed")
	if err := writeRunOutput(stdout, strings.TrimSpace(result), elapsed); err != nil {
		return err
	}
	if goalReached {
		return nil
	}
	return fmt.Errorf("goalkeeper validation did not pass: verdict=%s", verdict)
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

func newWorkflowAgent(worker, validator agent.Agent, maxIterations uint, logger zerolog.Logger) (agent.Agent, error) {
	workerStep, err := newLoggedStepAgent(worker, stepLogSpec{Index: 1, ID: workerAgentID}, logger)
	if err != nil {
		return nil, err
	}
	validatorStep, err := newLoggedStepAgent(validator, stepLogSpec{Index: 2, ID: validatorAgentID}, logger)
	if err != nil {
		return nil, err
	}
	return goalkeeperworkflow.New(goalkeeperworkflow.NewOptions(
		workerStep,
		validatorStep,
		goalkeeperworkflow.WithMaxIterations(maxIterations),
	))
}

func newLoggedStepAgent(inner agent.Agent, spec stepLogSpec, logger zerolog.Logger) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        inner.Name(),
		Description: inner.Description(),
		SubAgents:   inner.SubAgents(),
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return runLoggedStep(ctx, inner, spec, logger)
		},
	})
}

func runLoggedStep(
	ctx agent.InvocationContext,
	inner agent.Agent,
	spec stepLogSpec,
	logger zerolog.Logger,
) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		startedAt := time.Now()
		stepLogger := logger.With().
			Int("step_index", spec.Index).
			Str("step", spec.ID).
			Str("agent_name", inner.Name()).
			Str("invocation_id", ctx.InvocationID()).
			Str("session_id", ctx.Session().ID()).
			Logger()
		stepLogger.Info().Msg("goalkeeper step started")

		eventCount := 0
		responseLen := 0
		finalText := ""
		for ev, err := range inner.Run(ctx) {
			if err != nil {
				stepLogger.Error().
					Err(err).
					Int("event_count", eventCount).
					Dur("duration", time.Since(startedAt)).
					Msg("goalkeeper step failed")
				yield(nil, err)
				return
			}
			if ev != nil {
				eventCount++
				text := contentText(ev.Content)
				responseLen += len(text)
				if ev.IsFinalResponse() && text != "" {
					finalText = text
				}
			}
			if !yield(ev, nil) {
				return
			}
		}
		if trimmed := strings.TrimSpace(finalText); trimmed != "" {
			logger.Debug().
				Str("step", spec.ID).
				Str("text", trimmed).
				Msg("goalkeeper model final text")
		}
		stepLogger.Info().
			Int("event_count", eventCount).
			Int("response_len", responseLen).
			Dur("duration", time.Since(startedAt)).
			Msg("goalkeeper step completed")
	}
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
		"Use the available goal and context.",
		"Do the requested work in the current working directory.",
		"Return a concise plain-text summary of what changed and what evidence supports it.",
		"Run only lightweight sanity checks directly relevant to the work unless the goal asks for broader verification.",
	}, "\n")
}

func validatorInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper validator agent.",
		"Validate the prior worker result against the original user goal using the shared ADK session context.",
		"Inspect the current working directory as needed.",
		"Do not intentionally mutate files or continue the worker's implementation work.",
		"Start with exactly `verdict: pass` or `verdict: fail`.",
		"`verdict: pass` means the goal was reached.",
		"`verdict: fail` means the goal was not reached.",
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
		if part != nil && !part.Thought && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func parseVerdict(output string) (string, bool) {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	line := strings.ToLower(strings.TrimSpace(firstLine))
	switch line {
	case "verdict: pass":
		return "pass", true
	case "verdict: fail":
		return "fail", false
	default:
		return "unknown", false
	}
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
