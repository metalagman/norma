package goalkeeperactor

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
	"github.com/normahq/norma/actorlayer"
	"github.com/normahq/norma/actorlayer/adkactor"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/agentfactory"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultAgentType         = "codex_acp"
	defaultModel             = "gpt-5.3-codex"
	defaultMaxIterations     = uint(5)
	defaultStepAskTimeout    = 10 * time.Minute
	defaultCoordinatorAskTTL = 30 * time.Minute

	workerAgentID      = "worker"
	validatorAgentID   = "validator"
	workerAgentName    = "GoalkeeperWorker"
	validatorAgentName = "GoalkeeperValidator"

	workerActorID      = "goalkeeper-worker"
	validatorActorID   = "goalkeeper-validator"
	coordinatorActorID = "goalkeeper-coordinator"

	appName              = "goalkeeper-actor"
	userID               = "goalkeeper-user"
	headerConversationID = "conversation_id"
	headerUserID         = "user_id"
)

// Config configures the actor-based Goalkeeper playground run.
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
}

// Run executes the actor-based Goalkeeper playground workflow once.
func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, defaultDeps())
}

func defaultDeps() runtimeDeps {
	return runtimeDeps{newACPAgent: newACPAgent}
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
		Str("component", "playground.goalkeeper_actor").
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

	sys, err := actorlayer.NewSystem(actorlayer.Config{DefaultAskLimit: defaultStepAskTimeout})
	if err != nil {
		return fmt.Errorf("create actor system: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sys.Shutdown(shutdownCtx)
	}()

	sessions := session.InMemoryService()
	workerRef, err := sys.Spawn(ctx, workerActorID, adkactor.Props(adkactor.Config{
		AppName:           appName,
		Agent:             worker,
		SessionService:    sessions,
		RunConfig:         agent.RunConfig{},
		SessionPolicy:     conversationActorSession(headerConversationID, workerActorID),
		UserPolicy:        adkactor.HeaderUser(headerUserID, userID),
		Codec:             adkactor.TextCodec(),
		ReplyMode:         adkactor.ReplyFinal,
		AutoCreateSession: true,
	}), actorlayer.WithActorID(workerActorID))
	if err != nil {
		return fmt.Errorf("spawn worker actor: %w", err)
	}

	validatorRef, err := sys.Spawn(ctx, validatorActorID, adkactor.Props(adkactor.Config{
		AppName:           appName,
		Agent:             validator,
		SessionService:    sessions,
		RunConfig:         agent.RunConfig{},
		SessionPolicy:     conversationActorSession(headerConversationID, validatorActorID),
		UserPolicy:        adkactor.HeaderUser(headerUserID, userID),
		Codec:             adkactor.TextCodec(),
		ReplyMode:         adkactor.ReplyFinal,
		AutoCreateSession: true,
	}), actorlayer.WithActorID(validatorActorID))
	if err != nil {
		return fmt.Errorf("spawn validator actor: %w", err)
	}

	conversationID := fmt.Sprintf("goalkeeper-actor-%d", time.Now().UnixNano())
	coordinatorRef, err := sys.Spawn(ctx, coordinatorActorID, actorlayer.Props{
		Kind: "goalkeeper-coordinator",
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return &coordinatorBehavior{
				worker:         workerRef,
				validator:      validatorRef,
				maxIterations:  maxIterations,
				stepTimeout:    defaultStepAskTimeout,
				conversationID: conversationID,
				userID:         userID,
				logger:         logger,
			}, nil
		},
	}, actorlayer.WithActorID(coordinatorActorID))
	if err != nil {
		return fmt.Errorf("spawn coordinator actor: %w", err)
	}

	logger.Info().
		Str("working_dir", workingDir).
		Strs("command", command).
		Int("goal_len", len(goal)).
		Uint("max_iterations", maxIterations).
		Msg("goalkeeper actor started")

	reply, err := actorlayer.Ask(ctx, sys, coordinatorRef, goal,
		actorlayer.WithTimeout(defaultCoordinatorAskTTL),
		actorlayer.WithAskHeader(headerConversationID, conversationID),
		actorlayer.WithAskHeader(headerUserID, userID),
	)
	if err != nil {
		return err
	}

	result := strings.TrimSpace(payloadText(reply.Payload))
	elapsed := formatElapsed(time.Since(startedAt))
	verdict, goalReached := parseVerdict(result)

	logger.Info().
		Str("verdict", verdict).
		Bool("goal_reached", goalReached).
		Msg("goalkeeper validation completed")
	logger.Info().
		Bool("has_result", result != "").
		Str("elapsed", elapsed).
		Msg("goalkeeper actor completed")
	if err := writeRunOutput(stdout, result, elapsed); err != nil {
		return err
	}
	if goalReached {
		return nil
	}
	return fmt.Errorf("goalkeeper validation did not pass: verdict=%s", verdict)
}

func newACPAgent(ctx context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
	agentRegistry := map[string]agentconfig.Config{
		cfg.AgentID: {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd:   append([]string(nil), cfg.Command...),
				Model: defaultModel,
			},
		},
	}
	factory := agentfactory.New(
		agentRegistry,
		nil,
		agentfactory.WithPermissionHandler(autoAllowPermission),
		agentfactory.WithStderrWriter(cfg.Stderr),
	)
	buildCtx := withAgentContextLogger(ctx, cfg.Logger, cfg.AgentID)
	built, err := factory.Build(buildCtx, agentfactory.BuildRequest{
		AgentID:          cfg.AgentID,
		Name:             cfg.Name,
		Description:      cfg.Description,
		Instruction:      cfg.Instruction,
		WorkingDirectory: cfg.WorkingDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}
	if closable, ok := built.(closableAgent); ok {
		return closable, nil
	}
	return nonClosableAgent{Agent: built}, nil
}

func withAgentContextLogger(ctx context.Context, logger zerolog.Logger, agentID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	agentLogger := logger.With().Str("agent_id", strings.TrimSpace(agentID)).Logger()
	return agentLogger.WithContext(ctx)
}

type nonClosableAgent struct {
	agent.Agent
}

func (n nonClosableAgent) Close() error {
	return nil
}

type coordinatorBehavior struct {
	worker         actorlayer.Ref
	validator      actorlayer.Ref
	maxIterations  uint
	stepTimeout    time.Duration
	conversationID string
	userID         string
	logger         zerolog.Logger

	active             bool
	phase              coordinatorPhase
	goal               string
	iteration          int
	conversation       string
	user               string
	callerReplyTo      *actorlayer.Ref
	workerStartedAt    time.Time
	validatorStartedAt time.Time
}

func (b *coordinatorBehavior) Receive(ctx actorlayer.Context, env actorlayer.Envelope) error {
	sender := ctx.Sender()
	switch {
	case sender == nil:
		return b.handleGoal(ctx, env)
	case sender.ID() == workerActorID:
		return b.handleWorkerReply(ctx, env)
	case sender.ID() == validatorActorID:
		return b.handleValidatorReply(ctx, env)
	default:
		return b.replyError(ctx, env, fmt.Errorf("unexpected sender: %s", sender.ID()))
	}
}

type coordinatorPhase string

const (
	coordinatorPhaseIdle             coordinatorPhase = "idle"
	coordinatorPhaseWaitingWorker    coordinatorPhase = "waiting_worker"
	coordinatorPhaseWaitingValidator coordinatorPhase = "waiting_validator"
)

func (b *coordinatorBehavior) handleGoal(ctx actorlayer.Context, env actorlayer.Envelope) error {
	if b.active {
		return b.replyError(ctx, env, errors.New("goalkeeper actor run already in progress"))
	}
	goal := strings.TrimSpace(payloadText(env.Payload))
	if goal == "" {
		return b.replyError(ctx, env, errors.New("goal is required"))
	}
	if env.ReplyTo == nil {
		return b.replyError(ctx, env, errors.New("goal request requires reply target"))
	}

	conversation := b.conversationID
	if header := env.Headers[headerConversationID]; header != "" {
		conversation = header
	}
	user := b.userID
	if header := env.Headers[headerUserID]; header != "" {
		user = header
	}

	replyTo := *env.ReplyTo
	b.active = true
	b.phase = coordinatorPhaseWaitingWorker
	b.goal = goal
	b.iteration = 1
	b.conversation = conversation
	b.user = user
	b.callerReplyTo = &replyTo

	if err := b.dispatchWorker(ctx); err != nil {
		return b.failActiveRun(ctx, fmt.Errorf("worker step failed: %w", err))
	}
	return nil
}

func (b *coordinatorBehavior) handleWorkerReply(ctx actorlayer.Context, env actorlayer.Envelope) error {
	if !b.active || b.phase != coordinatorPhaseWaitingWorker {
		return b.failActiveRun(ctx, errors.New("received worker reply in invalid state"))
	}
	if replyErr, ok := env.Payload.(error); ok && replyErr != nil {
		b.logger.Error().
			Err(replyErr).
			Int("iteration", b.iteration).
			Str("step", workerAgentID).
			Dur("duration", time.Since(b.workerStartedAt)).
			Msg("goalkeeper actor step failed")
		return b.failActiveRun(ctx, fmt.Errorf("worker step failed: %w", replyErr))
	}

	workerOutput := strings.TrimSpace(payloadText(env.Payload))
	b.logger.Info().
		Int("iteration", b.iteration).
		Str("step", workerAgentID).
		Int("response_len", len(workerOutput)).
		Dur("duration", time.Since(b.workerStartedAt)).
		Msg("goalkeeper actor step completed")

	if err := b.dispatchValidator(ctx, workerOutput); err != nil {
		return b.failActiveRun(ctx, fmt.Errorf("validator step failed: %w", err))
	}
	return nil
}

func (b *coordinatorBehavior) handleValidatorReply(ctx actorlayer.Context, env actorlayer.Envelope) error {
	if !b.active || b.phase != coordinatorPhaseWaitingValidator {
		return b.failActiveRun(ctx, errors.New("received validator reply in invalid state"))
	}
	if replyErr, ok := env.Payload.(error); ok && replyErr != nil {
		b.logger.Error().
			Err(replyErr).
			Int("iteration", b.iteration).
			Str("step", validatorAgentID).
			Dur("duration", time.Since(b.validatorStartedAt)).
			Msg("goalkeeper actor step failed")
		return b.failActiveRun(ctx, fmt.Errorf("validator step failed: %w", replyErr))
	}

	validatorOutput := strings.TrimSpace(payloadText(env.Payload))
	b.logger.Info().
		Int("iteration", b.iteration).
		Str("step", validatorAgentID).
		Int("response_len", len(validatorOutput)).
		Dur("duration", time.Since(b.validatorStartedAt)).
		Msg("goalkeeper actor step completed")

	verdict, pass := parseVerdict(validatorOutput)
	if verdict == "unknown" {
		b.logger.Warn().
			Int("iteration", b.iteration).
			Str("step", validatorAgentID).
			Str("validator_output", validatorOutput).
			Msg("validator output missing required verdict prefix")
	}
	b.logger.Info().
		Int("iteration", b.iteration).
		Str("verdict", verdict).
		Bool("goal_reached", pass).
		Msg("goalkeeper actor iteration validated")

	if pass {
		return b.finishActiveRun(ctx, validatorOutput)
	}
	if b.iteration >= int(b.maxIterations) {
		return b.finishActiveRun(ctx, validatorOutput)
	}

	b.iteration++
	if err := b.dispatchWorker(ctx); err != nil {
		return b.failActiveRun(ctx, fmt.Errorf("worker step failed: %w", err))
	}
	return nil
}

func (b *coordinatorBehavior) dispatchWorker(ctx actorlayer.Context) error {
	prompt := formatGoalPrompt(b.goal)
	b.phase = coordinatorPhaseWaitingWorker
	b.workerStartedAt = time.Now()
	b.logger.Info().
		Int("iteration", b.iteration).
		Str("step", workerAgentID).
		Msg("goalkeeper actor step started")
	b.logger.Debug().
		Int("iteration", b.iteration).
		Str("step", workerAgentID).
		Str("prompt", prompt).
		Msg("goalkeeper actor worker prompt")

	return ctx.Tell(ctx, b.worker, prompt,
		actorlayer.WithReplyTo(ctx.Self()),
		actorlayer.WithHeader(headerConversationID, b.conversation),
		actorlayer.WithHeader(headerUserID, b.user),
	)
}

func (b *coordinatorBehavior) dispatchValidator(ctx actorlayer.Context, workerOutput string) error {
	prompt := formatValidatorPrompt(b.goal, workerOutput)
	b.phase = coordinatorPhaseWaitingValidator
	b.validatorStartedAt = time.Now()
	b.logger.Info().
		Int("iteration", b.iteration).
		Str("step", validatorAgentID).
		Msg("goalkeeper actor step started")

	return ctx.Tell(ctx, b.validator, prompt,
		actorlayer.WithReplyTo(ctx.Self()),
		actorlayer.WithHeader(headerConversationID, b.conversation),
		actorlayer.WithHeader(headerUserID, b.user),
	)
}

func (b *coordinatorBehavior) finishActiveRun(ctx actorlayer.Context, output string) error {
	replyTo := b.callerReplyTo
	b.resetRunState()
	if replyTo == nil {
		return nil
	}
	return ctx.Tell(ctx, *replyTo, output)
}

func (b *coordinatorBehavior) failActiveRun(ctx actorlayer.Context, err error) error {
	replyTo := b.callerReplyTo
	b.resetRunState()
	if replyTo == nil {
		return err
	}
	if tellErr := ctx.Tell(ctx, *replyTo, err); tellErr != nil {
		return tellErr
	}
	return nil
}

func (b *coordinatorBehavior) resetRunState() {
	b.active = false
	b.phase = coordinatorPhaseIdle
	b.goal = ""
	b.iteration = 0
	b.conversation = ""
	b.user = ""
	b.callerReplyTo = nil
	b.workerStartedAt = time.Time{}
	b.validatorStartedAt = time.Time{}
}

func (b *coordinatorBehavior) replyError(ctx actorlayer.Context, env actorlayer.Envelope, err error) error {
	if env.ReplyTo == nil {
		return err
	}
	if tellErr := ctx.Tell(ctx, *env.ReplyTo, err); tellErr != nil {
		return tellErr
	}
	return nil
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
	return strings.TrimSpace(goal)
}

type conversationActorSessionPolicy struct {
	headerName string
	actorID    string
}

func conversationActorSession(headerName string, actorID string) adkactor.SessionPolicy {
	return conversationActorSessionPolicy{
		headerName: headerName,
		actorID:    strings.TrimSpace(actorID),
	}
}

func (p conversationActorSessionPolicy) SessionID(env actorlayer.Envelope, self actorlayer.Ref) string {
	base := adkactor.ConversationSession(p.headerName).SessionID(env, self)
	role := p.actorID
	if role == "" {
		role = string(self.ID())
	}
	return base + "::" + role
}

func formatValidatorPrompt(goal string, workerOutput string) string {
	return strings.Join([]string{
		"Original goal:",
		strings.TrimSpace(goal),
		"",
		"Worker output:",
		strings.TrimSpace(workerOutput),
		"",
		"Evaluate whether the goal is fully achieved based on the worker output.",
		"Respond exactly with first line `verdict: pass` or `verdict: fail`, then concise evidence.",
	}, "\n")
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

func payloadText(payload any) string {
	switch value := payload.(type) {
	case nil:
		return ""
	case string:
		return value
	case []byte:
		return string(value)
	case *genai.Content:
		return contentText(value)
	case genai.Content:
		return contentText(&value)
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprintf("%v", value)
	}
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
