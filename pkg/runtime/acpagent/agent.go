package acpagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/pkg/runtime/sessionstate"
	"github.com/rs/zerolog"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// InstructionProvider allows ACP instructions to be created dynamically using
// invocation context, mirroring llmagent semantics.
type InstructionProvider func(ctx adkagent.ReadonlyContext) (string, error)

// Config configures an ACP-backed ADK agent.
type Config struct {
	// Context is the base context for the agent's lifecycle.
	Context context.Context
	// Name is the display name of the agent. Defaults to "ACPAgent".
	Name string
	// Description describes the agent's purpose.
	Description string
	// BeforeAgentCallbacks are standard ADK lifecycle callbacks invoked before
	// the ACP-backed run starts.
	BeforeAgentCallbacks []adkagent.BeforeAgentCallback
	// AfterAgentCallbacks are standard ADK lifecycle callbacks invoked after
	// the ACP-backed run completes.
	AfterAgentCallbacks []adkagent.AfterAgentCallback
	// Model is the specific LLM model identifier to use.
	Model string
	// Mode is the ACP session mode identifier to use.
	Mode string
	// Instruction is the optional instruction applied to each invocation.
	Instruction string
	// GlobalInstruction is the optional global instruction applied before
	// Instruction.
	GlobalInstruction string
	// InstructionProvider dynamically provides [Config.Instruction] content.
	// When set, this takes precedence over [Config.Instruction].
	InstructionProvider InstructionProvider
	// GlobalInstructionProvider dynamically provides
	// [Config.GlobalInstruction] content. When set, this takes precedence over
	// [Config.GlobalInstruction].
	GlobalInstructionProvider InstructionProvider
	// SystemInstructions is deprecated and kept for backward compatibility.
	// Use Instruction instead.
	SystemInstructions string
	// ClientName is the name reported to the ACP server during initialization.
	ClientName string
	// ClientVersion is the version reported to the ACP server during initialization.
	ClientVersion string
	// Command is the argv array used to start the ACP subprocess.
	Command []string
	// WorkingDir is the default directory for ACP execution:
	//   - the ACP subprocess is started with this directory as cmd.Dir.
	//   - ACP session/new uses this as cwd unless overridden per ADK session
	//     via session state key [sessionstate.CWDKey].
	//
	// When session state override is present, the override takes precedence for
	// ACP session cwd selection.
	WorkingDir string
	// Stderr is an optional writer for the ACP subprocess's standard error.
	Stderr io.Writer
	// PermissionHandler decides how to respond to ACP permission requests.
	PermissionHandler PermissionHandler
	// Logger is the zerolog logger to use for this agent.
	Logger *zerolog.Logger
	// MCPServers is the map of MCP server configurations.
	MCPServers map[string]MCPServerConfig
	// SessionService is ignored. ACP session bindings are recorded through the
	// ADK session state exposed by the invocation context.
	SessionService session.Service
	// SessionID is an optional desired session ID to request when creating ACP sessions.
	// It is sent via session/new _meta.sessionId and may be ignored by some ACP runtimes.
	SessionID string
	// OutputKey stores the final visible model output in session state delta for this invocation.
	// When set, the final non-partial turn-complete event includes
	// event.Actions.StateDelta[OutputKey] = final visible output text.
	OutputKey string
}

// Agent adapts an Agentic Computing Protocol (ACP) runtime to the ADK agent interface.
// It manages the lifecycle of an ACP subprocess and maps ACP sessions to ADK sessions.
type Agent struct {
	adkagent.Agent

	client                    *Client
	workingDir                string
	sessionModel              string
	sessionMode               string
	sessionID                 string
	outputKey                 string
	instruction               string
	globalInstruction         string
	instructionProvider       InstructionProvider
	globalInstructionProvider InstructionProvider
	logger                    zerolog.Logger
	sessionMu                 sync.Mutex
	bindingByADK              map[string]acpSessionBinding
	mcpServers                []acp.McpServer
}

type acpSessionBinding struct {
	remoteSessionID string
	cwd             string
	metaJSON        string
	instructionInit instructionInitState
	initWait        chan struct{}
}

type acpSessionConfig struct {
	sessionID string
	cwd       string
	meta      map[string]any
	metaJSON  string
}

type instructionInitState uint8

const (
	instructionInitPending instructionInitState = iota
	instructionInitInProgress
	instructionInitDone
)

const (
	defaultAgentName        = "ACPAgent"
	defaultAgentDescription = "ACP runtime exposed through ADK"

	acpTypeText       = "text"
	acpTypeImage      = "image"
	acpTypeAudio      = "audio"
	acpTypeResource   = "resource"
	acpUsageUpdate    = "usage_update"
	acpPlanEntriesKey = "entries"
)

const (
	// SessionStateKey is the reserved ADK session-state key for ACP-specific
	// per-session settings.
	//
	// The value at this key must be an object with optional fields:
	//   - "meta" (object): forwarded to ACP session/new._meta
	//   - "session_id" (string): ACP session id persisted by this adapter
	//     for diagnostics and downstream visibility.
	//   - "persisted_session_id" (string): canonical ACP session id persisted
	//     by this adapter and used for ACP session/resume.
	//
	// Set it before the first invocation in a given ADK session; once that ADK
	// session is bound to an ACP session, later changes do not rebind.
	SessionStateKey = "acp_session"
	// PlanStateKey is the ADK session-state key used for ACP plan snapshots.
	//
	// Each ACP session/update.plan notification is projected into
	// event.Actions.StateDelta[PlanStateKey] as the authoritative full plan
	// replacement snapshot.
	PlanStateKey = "acp_plan"
	// persistedSessionIDKey stores the canonical ACP session id used for resume.
	persistedSessionIDKey = "persisted_session_id"
)

var _ adkagent.Agent = (*Agent)(nil)

var placeholderRegex = regexp.MustCompile(`{+[^{}]*}+`)

const (
	appPrefix  = "app:"
	userPrefix = "user:"
	tempPrefix = "temp:"
)

// New creates an ADK agent backed by an ACP client process.
//
// It starts the ACP process, performs ACP initialization, and creates ACP
// sessions lazily per ADK session.
//
// Per ADK session, callers may provide state overrides:
//   - [sessionstate.CWDKey] (string): override ACP session/new cwd
//   - [SessionStateKey].meta (object): forwarded to ACP session/new._meta and
//     session/resume._meta
//
// ACP session IDs are agent-owned. The adapter persists a canonical ACP session
// id under [SessionStateKey].persisted_session_id and uses that value for
// session/resume (falling back to session/load when resume is unsupported).
// [SessionStateKey].session_id is persisted as a mirror for visibility and is
// not authoritative for resume targeting.
//
// Restored sessions always reapply startup instructions on the first
// post-resume invocation so session-start context derived from current ADK
// state is refreshed before the user prompt runs.
//
// If no override is provided, Config.WorkingDir is used as ACP session cwd.
// The first ACP session created for an ADK session is reused for subsequent
// invocations in that same ADK session.
//
// The caller is responsible for calling Close() to shut down the subprocess.
func New(cfg Config) (*Agent, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = defaultAgentName
	}
	if strings.TrimSpace(cfg.Description) == "" {
		cfg.Description = defaultAgentDescription
	}

	l := zerolog.Nop()
	if cfg.Logger != nil {
		l = cfg.Logger.With().Str("subcomponent", "acpagent.agent").Logger()
	}

	client, err := NewClient(ctx, ClientConfig{
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		ClientName:        cfg.ClientName,
		ClientVersion:     cfg.ClientVersion,
		Stderr:            cfg.Stderr,
		PermissionHandler: cfg.PermissionHandler,
		Logger:            cfg.Logger,
		SessionID:         cfg.SessionID,
	})
	if err != nil {
		return nil, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		if closeErr := client.Close(); closeErr != nil {
			l.Error().Err(closeErr).Msg("failed to close acp client after initialize failure")
		}
		return nil, fmt.Errorf("initialize acp client: %w", err)
	}

	mcpServers, err := convertMCPServers(cfg.MCPServers)
	if err != nil {
		if closeErr := client.Close(); closeErr != nil {
			l.Error().Err(closeErr).Msg("failed to close acp client after mcp config conversion failure")
		}
		return nil, fmt.Errorf("convert mcp servers: %w", err)
	}

	a := &Agent{
		client:                    client,
		workingDir:                cfg.WorkingDir,
		sessionModel:              strings.TrimSpace(cfg.Model),
		sessionMode:               strings.TrimSpace(cfg.Mode),
		sessionID:                 strings.TrimSpace(cfg.SessionID),
		outputKey:                 strings.TrimSpace(cfg.OutputKey),
		instruction:               normalizeInstruction(cfg.Instruction, cfg.SystemInstructions),
		globalInstruction:         strings.TrimSpace(cfg.GlobalInstruction),
		instructionProvider:       cfg.InstructionProvider,
		globalInstructionProvider: cfg.GlobalInstructionProvider,
		logger:                    l,
		bindingByADK:              make(map[string]acpSessionBinding),
		mcpServers:                mcpServers,
	}
	base, err := adkagent.New(adkagent.Config{
		Name:                 cfg.Name,
		Description:          cfg.Description,
		BeforeAgentCallbacks: cfg.BeforeAgentCallbacks,
		Run:                  a.run,
		AfterAgentCallbacks:  cfg.AfterAgentCallbacks,
	})
	if err != nil {
		if closeErr := client.Close(); closeErr != nil {
			l.Error().Err(closeErr).Msg("failed to close acp client after adk agent creation failure")
		}
		return nil, fmt.Errorf("create adk acp agent: %w", err)
	}
	a.Agent = base
	return a, nil
}

// Close shuts down the underlying ACP client process.
func (a *Agent) Close() error {
	if err := a.client.Close(); err != nil {
		return fmt.Errorf("close acp client: %w", err)
	}
	return nil
}

func (a *Agent) run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		logger := a.invocationLogger(ctx)

		instructions, err := a.resolveInstructions(ctx)
		if err != nil {
			yield(nil, err)
			return
		}

		prompt := extractPromptText(ctx.UserContent())
		if strings.TrimSpace(prompt) == "" {
			yield(nil, errors.New("prompt is empty"))
			return
		}

		adkSessionID := ctx.Session().ID()
		remoteSessionID, err := a.ensureRemoteSession(ctx, logger, adkSessionID)
		if err != nil {
			yield(nil, err)
			return
		}
		if err := a.ensureInstructionInitialized(ctx, logger, adkSessionID, remoteSessionID, instructions); err != nil {
			yield(nil, err)
			return
		}

		logger.Debug().
			Str("adk_session_id", adkSessionID).
			Str("acp_session_id", remoteSessionID).
			Int("prompt_len", len(prompt)).
			Msg("starting adk invocation")

		updates, resultCh, err := a.client.Prompt(ctx, remoteSessionID, prompt)
		if err != nil {
			yield(nil, err)
			return
		}

		var promptResult *PromptResult
		var finalText strings.Builder
		var latestPlanSnapshot map[string]any
		for updates != nil || resultCh != nil {
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			case ext, ok := <-updates:
				if !ok {
					updates = nil
					continue
				}
				ev, ok := mapACPUpdateToEvent(logger, ctx.InvocationID(), ext)
				if !ok {
					continue
				}
				if ext.Update.AgentMessageChunk != nil {
					finalText.WriteString(contentVisibleText(ev.Content))
				}
				if planSnapshot, ok := ev.Actions.StateDelta[PlanStateKey].(map[string]any); ok {
					latestPlanSnapshot = planSnapshot
				}
				// We log but don't re-mark as partial here as mapACPUpdateToEvent
				// already set the appropriate Partial flag.
				a.logADKEvent(logger, ev, "yielding adk event")
				if !yield(ev, nil) {
					return
				}
			case result, ok := <-resultCh:
				if !ok {
					resultCh = nil
					continue
				}
				promptResult = &result
				resultCh = nil
			}
		}

		if promptResult != nil && promptResult.Err != nil {
			yield(nil, promptResult.Err)
			return
		}

		logger.Debug().
			Str("adk_session_id", adkSessionID).
			Str("acp_session_id", remoteSessionID).
			Msg("completed adk invocation")

		ev := session.NewEvent(ctx.InvocationID())
		if promptResult != nil {
			ev.FinishReason = mapACPStopReasonToFinishReason(promptResult.Response.StopReason)
			ev.UsageMetadata = mapACPUsageToUsageMetadata(promptResult.Usage)
		}
		finalOutput := finalText.String()
		if finalOutput != "" {
			ev.Content = genai.NewContentFromText(finalOutput, genai.RoleModel)
		}
		if latestPlanSnapshot != nil {
			ev.Actions.StateDelta[PlanStateKey] = latestPlanSnapshot
		}
		a.maybePersistSessionState(ev, adkSessionID)
		a.maybeSaveOutputToState(ev, finalOutput)
		ev.TurnComplete = true
		a.logADKEvent(logger, ev, "yielding final turn complete event")
		if !yield(ev, nil) {
			return
		}
	}
}

func (a *Agent) maybeSaveOutputToState(event *session.Event, output string) {
	if a.outputKey == "" || event == nil || event.Partial || output == "" {
		return
	}
	if event.Actions.StateDelta == nil {
		event.Actions.StateDelta = make(map[string]any)
	}
	event.Actions.StateDelta[a.outputKey] = output
}

func (a *Agent) maybePersistSessionState(event *session.Event, adkSessionID string) {
	if event == nil || event.Partial {
		return
	}

	a.sessionMu.Lock()
	binding, ok := a.bindingByADK[adkSessionID]
	a.sessionMu.Unlock()
	if !ok || strings.TrimSpace(binding.remoteSessionID) == "" {
		return
	}

	acpState := buildACPState(binding.remoteSessionID, binding.metaJSON)

	if event.Actions.StateDelta == nil {
		event.Actions.StateDelta = make(map[string]any)
	}
	event.Actions.StateDelta[SessionStateKey] = acpState
}

func (a *Agent) ensureInstructionInitialized(
	ctx adkagent.InvocationContext,
	logger zerolog.Logger,
	adkSessionID string,
	remoteSessionID string,
	instructions string,
) error {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return nil
	}

	for {
		a.sessionMu.Lock()
		binding, ok := a.bindingByADK[adkSessionID]
		if !ok || binding.remoteSessionID == "" {
			a.sessionMu.Unlock()
			return fmt.Errorf("acp session binding is missing for adk session %q", adkSessionID)
		}
		if binding.remoteSessionID != remoteSessionID {
			a.sessionMu.Unlock()
			return fmt.Errorf(
				"acp session binding mismatch for adk session %q: got %q want %q",
				adkSessionID,
				binding.remoteSessionID,
				remoteSessionID,
			)
		}

		switch binding.instructionInit {
		case instructionInitDone:
			a.sessionMu.Unlock()
			return nil
		case instructionInitInProgress:
			waitCh := binding.initWait
			a.sessionMu.Unlock()
			if waitCh == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-waitCh:
				continue
			}
		case instructionInitPending:
			waitCh := make(chan struct{})
			binding.instructionInit = instructionInitInProgress
			binding.initWait = waitCh
			a.bindingByADK[adkSessionID] = binding
			a.sessionMu.Unlock()

			initErr := a.runInstructionBootstrap(ctx, logger, adkSessionID, remoteSessionID, instructions)

			a.sessionMu.Lock()
			updated, ok := a.bindingByADK[adkSessionID]
			if ok && updated.remoteSessionID == remoteSessionID {
				doneCh := updated.initWait
				updated.initWait = nil
				if initErr == nil {
					updated.instructionInit = instructionInitDone
				} else {
					updated.instructionInit = instructionInitPending
				}
				a.bindingByADK[adkSessionID] = updated
				a.sessionMu.Unlock()
				if doneCh != nil {
					close(doneCh)
				}
			} else {
				a.sessionMu.Unlock()
				close(waitCh)
			}

			if initErr != nil {
				return initErr
			}
			return nil
		}
	}
}

func (a *Agent) runInstructionBootstrap(
	ctx adkagent.InvocationContext,
	logger zerolog.Logger,
	adkSessionID string,
	remoteSessionID string,
	instructions string,
) error {
	logger.Debug().
		Str("adk_session_id", adkSessionID).
		Str("acp_session_id", remoteSessionID).
		Int("instruction_len", len(instructions)).
		Msg("initializing acp session instructions")

	bootstrapCtx := context.WithValue(ctx, suppressLastChunkLogContextKey, true)
	updates, resultCh, err := a.client.Prompt(bootstrapCtx, remoteSessionID, instructions)
	if err != nil {
		return fmt.Errorf("initialize acp session instructions: %w", err)
	}

	var promptResult *PromptResult
	for updates != nil || resultCh != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-updates:
			if !ok {
				updates = nil
			}
		case result, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			promptResult = &result
			resultCh = nil
		}
	}

	if promptResult != nil && promptResult.Err != nil {
		return fmt.Errorf("initialize acp session instructions: %w", promptResult.Err)
	}

	logger.Debug().
		Str("adk_session_id", adkSessionID).
		Str("acp_session_id", remoteSessionID).
		Msg("acp session instructions initialized")
	return nil
}

func (a *Agent) invocationLogger(ctx context.Context) zerolog.Logger {
	if ctx == nil {
		return a.logger
	}
	ctxLogger := zerolog.Ctx(ctx)
	if ctxLogger == nil || ctxLogger == zerolog.DefaultContextLogger || ctxLogger.GetLevel() == zerolog.Disabled {
		return a.logger
	}
	return ctxLogger.With().Str("subcomponent", "acpagent.agent").Logger()
}

func (a *Agent) logADKEvent(logger zerolog.Logger, ev *session.Event, msg string) {
	if ev == nil {
		return
	}
	logEvent := logger.Trace().
		Str("invocation_id", ev.InvocationID).
		Bool("partial", ev.Partial).
		Bool("turn_complete", ev.TurnComplete)

	if ev.FinishReason != "" {
		logEvent = logEvent.Str("finish_reason", string(ev.FinishReason))
	}
	if ev.Content != nil {
		logEvent = logEvent.Bool("has_content", true)
	}
	logEvent.Msg(msg)
}

func mapACPStopReasonToFinishReason(reason acp.StopReason) genai.FinishReason {
	switch reason {
	case acp.StopReasonEndTurn:
		return genai.FinishReasonStop
	case acp.StopReasonMaxTokens:
		return genai.FinishReasonMaxTokens
	case acp.StopReasonRefusal:
		return genai.FinishReasonProhibitedContent
	case acp.StopReasonCancelled, acp.StopReasonMaxTurnRequests:
		return genai.FinishReasonOther // No direct match for cancelled in genai.FinishReason
	default:
		return genai.FinishReasonUnspecified
	}
}

func mapACPUsageToUsageMetadata(usage *acp.Usage) *genai.GenerateContentResponseUsageMetadata {
	if usage == nil {
		return nil
	}
	m := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(usage.InputTokens),
		CandidatesTokenCount: int32(usage.OutputTokens),
		TotalTokenCount:      int32(usage.TotalTokens),
	}
	if usage.CachedReadTokens != nil {
		m.CachedContentTokenCount = int32(*usage.CachedReadTokens)
	}
	return m
}

func mapACPLegacyUsageToUsageMetadata(usage map[string]any) *genai.GenerateContentResponseUsageMetadata {
	if usage == nil {
		return nil
	}
	m := &genai.GenerateContentResponseUsageMetadata{}
	found := false
	if val, ok := usage["inputTokens"].(float64); ok {
		m.PromptTokenCount = int32(val)
		found = true
	}
	if val, ok := usage["outputTokens"].(float64); ok {
		m.CandidatesTokenCount = int32(val)
		found = true
	}
	if val, ok := usage["totalTokens"].(float64); ok {
		m.TotalTokenCount = int32(val)
		found = true
	}
	if val, ok := usage["cachedReadTokens"].(float64); ok {
		m.CachedContentTokenCount = int32(val)
		found = true
	}
	if !found {
		return nil
	}
	return m
}

func (a *Agent) ensureRemoteSession(ctx adkagent.InvocationContext, logger zerolog.Logger, adkSessionID string) (string, error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()

	cfg, err := a.resolveSessionConfig(ctx)
	if err != nil {
		return "", err
	}

	if binding, ok := a.bindingByADK[adkSessionID]; ok && binding.remoteSessionID != "" {
		if binding.cwd != cfg.cwd || binding.metaJSON != cfg.metaJSON || (cfg.sessionID != "" && binding.remoteSessionID != cfg.sessionID) {
			logger.Warn().
				Str("adk_session_id", adkSessionID).
				Str("acp_session_id", binding.remoteSessionID).
				Str("bound_cwd", binding.cwd).
				Str("requested_cwd", cfg.cwd).
				Str("requested_session_id", cfg.sessionID).
				RawJSON("bound_meta", []byte(binding.metaJSON)).
				RawJSON("requested_meta", []byte(cfg.metaJSON)).
				Msg("acp session config changed for existing adk session; keeping existing acp session binding")
		}
		logger.Debug().
			Str("adk_session_id", adkSessionID).
			Str("acp_session_id", binding.remoteSessionID).
			Msg("reusing acp session for adk session")
		return binding.remoteSessionID, nil
	}

	if cfg.sessionID != "" {
		resumeResp, err := a.client.ResumeSessionWithMeta(ctx, cfg.sessionID, cfg.cwd, a.mcpServers, cfg.meta)
		if err == nil {
			if err := a.client.applySessionModelAndMode(ctx, cfg.sessionID, a.sessionModel, a.sessionMode, resumeResp.Models, resumeResp.Modes); err != nil {
				return "", err
			}
			a.bindRemoteSession(adkSessionID, cfg.sessionID, cfg.cwd, cfg.metaJSON, instructionInitPending)
			a.logBoundRemoteSession(logger, "resumed acp session for adk session", adkSessionID, cfg.sessionID, cfg.cwd, cfg.metaJSON)
			if err := a.persistRemoteSessionBinding(ctx, cfg.sessionID, cfg.metaJSON); err != nil {
				return "", err
			}
			return cfg.sessionID, nil
		}

		if isACPMethodNotFoundError(err) {
			loadResp, loadErr := a.client.LoadSessionWithMeta(ctx, cfg.sessionID, cfg.cwd, a.mcpServers, cfg.meta)
			if loadErr == nil {
				if err := a.client.applySessionModelAndMode(ctx, cfg.sessionID, a.sessionModel, a.sessionMode, loadResp.Models, loadResp.Modes); err != nil {
					return "", err
				}
				a.bindRemoteSession(adkSessionID, cfg.sessionID, cfg.cwd, cfg.metaJSON, instructionInitDone)
				a.logBoundRemoteSession(logger, "loaded acp session for adk session", adkSessionID, cfg.sessionID, cfg.cwd, cfg.metaJSON)
				if err := a.persistRemoteSessionBinding(ctx, cfg.sessionID, cfg.metaJSON); err != nil {
					return "", err
				}
				return cfg.sessionID, nil
			}
			if !isACPMethodNotFoundError(loadErr) && !isACPSessionNotFoundError(loadErr) {
				return "", fmt.Errorf("load acp session %q: %w", cfg.sessionID, loadErr)
			}
			logger.Warn().
				Err(loadErr).
				Str("adk_session_id", adkSessionID).
				Str("acp_session_id", cfg.sessionID).
				Msg("acp session load unavailable; falling back to session/new")
		} else if !isACPSessionNotFoundError(err) {
			return "", fmt.Errorf("resume acp session %q: %w", cfg.sessionID, err)
		}
		logger.Warn().
			Err(err).
			Str("adk_session_id", adkSessionID).
			Str("acp_session_id", cfg.sessionID).
			Msg("acp session resume unavailable; falling back to session/new")
	}

	resp, err := a.client.CreateSessionWithMeta(ctx, cfg.cwd, a.sessionModel, a.sessionMode, a.mcpServers, cfg.meta)
	if err != nil {
		return "", err
	}
	sessionID := string(resp.SessionId)
	a.bindRemoteSession(adkSessionID, sessionID, cfg.cwd, cfg.metaJSON, instructionInitPending)
	a.logBoundRemoteSession(logger, "created new acp session for adk session", adkSessionID, sessionID, cfg.cwd, cfg.metaJSON)
	if err := a.persistRemoteSessionBinding(ctx, sessionID, cfg.metaJSON); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (a *Agent) bindRemoteSession(
	adkSessionID string,
	remoteSessionID string,
	cwd string,
	metaJSON string,
	initState instructionInitState,
) {
	a.bindingByADK[adkSessionID] = acpSessionBinding{
		remoteSessionID: remoteSessionID,
		cwd:             cwd,
		metaJSON:        metaJSON,
		instructionInit: initState,
	}
}

func (a *Agent) logBoundRemoteSession(
	logger zerolog.Logger,
	message string,
	adkSessionID string,
	remoteSessionID string,
	cwd string,
	metaJSON string,
) {
	event := logger.Debug().
		Str("adk_session_id", adkSessionID).
		Str("acp_session_id", remoteSessionID).
		Str("cwd", cwd).
		RawJSON("meta", []byte(metaJSON))
	if a.sessionModel != "" {
		event = event.Str("model", a.sessionModel)
	}
	if a.sessionMode != "" {
		event = event.Str("mode", a.sessionMode)
	}
	event.Msg(message)
}

func buildACPState(remoteSessionID, metaJSON string) map[string]any {
	acpState := map[string]any{
		"session_id":          remoteSessionID,
		persistedSessionIDKey: remoteSessionID,
	}
	if strings.TrimSpace(metaJSON) == "" || metaJSON == "{}" {
		return acpState
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(metaJSON), &meta); err == nil && len(meta) > 0 {
		acpState["meta"] = meta
	}
	return acpState
}

func (a *Agent) persistRemoteSessionBinding(
	ctx adkagent.InvocationContext,
	remoteSessionID string,
	metaJSON string,
) error {
	if ctx == nil || ctx.Session() == nil || strings.TrimSpace(remoteSessionID) == "" {
		return nil
	}

	acpState := buildACPState(remoteSessionID, metaJSON)
	if currentACPStateMatches(ctx.Session(), remoteSessionID, metaJSON) {
		if err := ctx.Session().State().Set(SessionStateKey, cloneAnyMap(acpState)); err != nil {
			return fmt.Errorf("set live acp session state: %w", err)
		}
		return nil
	}

	if err := ctx.Session().State().Set(SessionStateKey, cloneAnyMap(acpState)); err != nil {
		return fmt.Errorf("set live acp session state: %w", err)
	}
	return nil
}

func currentACPStateMatches(sess session.Session, remoteSessionID, metaJSON string) bool {
	if sess == nil {
		return false
	}
	rawState, err := sess.State().Get(SessionStateKey)
	if err != nil {
		return false
	}
	state, ok := rawState.(map[string]any)
	if !ok {
		return false
	}
	storedSessionID, _ := state["session_id"].(string)
	storedPersistedSessionID, _ := state[persistedSessionIDKey].(string)
	if strings.TrimSpace(storedSessionID) != strings.TrimSpace(remoteSessionID) {
		return false
	}
	if strings.TrimSpace(storedPersistedSessionID) != strings.TrimSpace(remoteSessionID) {
		return false
	}
	return normalizeACPStateMetaJSON(state["meta"]) == normalizeACPStateMetaJSONFromRaw(metaJSON)
}

func normalizeACPStateMetaJSON(raw any) string {
	if raw == nil {
		return "{}"
	}
	switch value := raw.(type) {
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "{}"
		}
		return string(encoded)
	default:
		return "{}"
	}
}

func normalizeACPStateMetaJSONFromRaw(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return "{}"
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(trimmed), &meta); err != nil {
		return "{}"
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (a *Agent) resolveSessionConfig(ctx adkagent.InvocationContext) (acpSessionConfig, error) {
	cfg := acpSessionConfig{
		cwd: strings.TrimSpace(a.workingDir),
	}
	rawCWD, err := ctx.Session().State().Get(sessionstate.CWDKey)
	if err != nil {
		if !errors.Is(err, session.ErrStateKeyNotExist) {
			return acpSessionConfig{}, fmt.Errorf("read %q from adk session state: %w", sessionstate.CWDKey, err)
		}
	} else {
		cwd, ok := rawCWD.(string)
		if !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q must be a string; got %T", sessionstate.CWDKey, rawCWD)
		}
		cfg.cwd = strings.TrimSpace(cwd)
	}

	rawState, err := ctx.Session().State().Get(SessionStateKey)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return normalizeACPConfigCWD(cfg)
		}
		return acpSessionConfig{}, fmt.Errorf("read %q from adk session state: %w", SessionStateKey, err)
	}

	state, ok := rawState.(map[string]any)
	if !ok {
		return acpSessionConfig{}, fmt.Errorf("adk session state %q must be an object; got %T", SessionStateKey, rawState)
	}
	if rawMeta, ok := state["meta"]; ok {
		meta, ok := rawMeta.(map[string]any)
		if !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q.meta must be an object; got %T", SessionStateKey, rawMeta)
		}
		cfg.meta = cloneAnyMap(meta)
	}
	if rawSessionID, ok := state["session_id"]; ok {
		if _, ok := rawSessionID.(string); !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q.session_id must be a string; got %T", SessionStateKey, rawSessionID)
		}
	}
	if rawPersistedSessionID, ok := state[persistedSessionIDKey]; ok {
		persistedSessionID, ok := rawPersistedSessionID.(string)
		if !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q.%s must be a string; got %T", SessionStateKey, persistedSessionIDKey, rawPersistedSessionID)
		}
		cfg.sessionID = strings.TrimSpace(persistedSessionID)
	}

	return normalizeACPConfigCWD(cfg)
}

func normalizeACPConfigCWD(cfg acpSessionConfig) (acpSessionConfig, error) {
	if cfg.meta == nil {
		cfg.metaJSON = "{}"
	} else {
		metaJSON, err := json.Marshal(cfg.meta)
		if err != nil {
			return acpSessionConfig{}, fmt.Errorf("marshal acp session meta: %w", err)
		}
		cfg.metaJSON = string(metaJSON)
	}

	if cfg.cwd == "" {
		return acpSessionConfig{}, fmt.Errorf("acp session cwd is empty")
	}
	absCWD, err := filepath.Abs(cfg.cwd)
	if err != nil {
		return acpSessionConfig{}, fmt.Errorf("resolve acp session cwd %q: %w", cfg.cwd, err)
	}
	info, err := os.Stat(absCWD)
	if err != nil {
		return acpSessionConfig{}, fmt.Errorf("stat acp session cwd %q: %w", absCWD, err)
	}
	if !info.IsDir() {
		return acpSessionConfig{}, fmt.Errorf("acp session cwd %q is not a directory", absCWD)
	}
	cfg.cwd = absCWD
	return cfg, nil
}

func normalizeInstruction(primary, deprecated string) string {
	inst := strings.TrimSpace(primary)
	if inst != "" {
		return inst
	}
	return strings.TrimSpace(deprecated)
}

func (a *Agent) resolveInstructions(ctx adkagent.InvocationContext) (string, error) {
	readonlyCtx := readonlyInvocationContext{invocation: ctx}
	instructions := make([]string, 0, 2)

	globalInstruction, err := a.resolveSingleInstruction(
		ctx,
		readonlyCtx,
		a.globalInstruction,
		a.globalInstructionProvider,
		"global instruction",
	)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(globalInstruction) != "" {
		instructions = append(instructions, globalInstruction)
	}

	instruction, err := a.resolveSingleInstruction(
		ctx,
		readonlyCtx,
		a.instruction,
		a.instructionProvider,
		"instruction",
	)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(instruction) != "" {
		instructions = append(instructions, instruction)
	}

	return strings.Join(instructions, "\n\n"), nil
}

func (a *Agent) resolveSingleInstruction(
	invocationCtx adkagent.InvocationContext,
	ctx adkagent.ReadonlyContext,
	templateInstruction string,
	provider InstructionProvider,
	kind string,
) (string, error) {
	if provider != nil {
		instruction, err := provider(ctx)
		if err != nil {
			return "", fmt.Errorf("evaluate %s provider: %w", kind, err)
		}
		return instruction, nil
	}

	templateInstruction = strings.TrimSpace(templateInstruction)
	if templateInstruction == "" {
		return "", nil
	}

	instruction, err := injectSessionState(invocationCtx, templateInstruction)
	if err != nil {
		return "", fmt.Errorf("inject session state into %s: %w", kind, err)
	}
	return instruction, nil
}

func injectSessionState(ctx adkagent.InvocationContext, templateInstruction string) (string, error) {
	var result strings.Builder
	lastIndex := 0
	matches := placeholderRegex.FindAllStringIndex(templateInstruction, -1)

	for _, matchIndexes := range matches {
		startIndex, endIndex := matchIndexes[0], matchIndexes[1]
		result.WriteString(templateInstruction[lastIndex:startIndex])

		replacement, err := replaceTemplateMatch(ctx, templateInstruction[startIndex:endIndex])
		if err != nil {
			return "", err
		}
		result.WriteString(replacement)

		lastIndex = endIndex
	}

	result.WriteString(templateInstruction[lastIndex:])
	return result.String(), nil
}

func replaceTemplateMatch(ctx adkagent.InvocationContext, match string) (string, error) {
	varName := strings.TrimSpace(strings.Trim(match, "{}"))
	optional := false
	if strings.HasSuffix(varName, "?") {
		optional = true
		varName = strings.TrimSuffix(varName, "?")
	}

	if after, ok := strings.CutPrefix(varName, "artifact."); ok {
		if ctx.Artifacts() == nil {
			return "", fmt.Errorf("artifact service is not initialized")
		}
		resp, err := ctx.Artifacts().Load(ctx, after)
		if err != nil {
			if optional {
				return "", nil
			}
			return "", fmt.Errorf("failed to load artifact %s: %w", after, err)
		}
		return resp.Part.Text, nil
	}

	if !isValidStateName(varName) {
		return match, nil
	}

	value, err := ctx.Session().State().Get(varName)
	if err != nil {
		if optional {
			return "", nil
		}
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return fmt.Sprintf("%v", value), nil
}

func isValidStateName(varName string) bool {
	parts := strings.Split(varName, ":")
	if len(parts) == 1 {
		return isIdentifier(varName)
	}

	if len(parts) == 2 {
		prefix := parts[0] + ":"
		validPrefixes := []string{appPrefix, userPrefix, tempPrefix}
		if slices.Contains(validPrefixes, prefix) {
			return isIdentifier(parts[1])
		}
	}
	return false
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

type readonlyInvocationContext struct {
	invocation adkagent.InvocationContext
}

func (c readonlyInvocationContext) Deadline() (time.Time, bool) {
	if c.invocation == nil {
		return time.Time{}, false
	}
	return c.invocation.Deadline()
}

func (c readonlyInvocationContext) Done() <-chan struct{} {
	if c.invocation == nil {
		return nil
	}
	return c.invocation.Done()
}

func (c readonlyInvocationContext) Err() error {
	if c.invocation == nil {
		return nil
	}
	return c.invocation.Err()
}

func (c readonlyInvocationContext) Value(key any) any {
	if c.invocation == nil {
		return nil
	}
	return c.invocation.Value(key)
}

func (c readonlyInvocationContext) UserContent() *genai.Content {
	if c.invocation == nil {
		return nil
	}
	return c.invocation.UserContent()
}

func (c readonlyInvocationContext) InvocationID() string {
	if c.invocation == nil {
		return ""
	}
	return c.invocation.InvocationID()
}

func (c readonlyInvocationContext) AgentName() string {
	if c.invocation == nil || c.invocation.Agent() == nil {
		return ""
	}
	return c.invocation.Agent().Name()
}

func (c readonlyInvocationContext) ReadonlyState() session.ReadonlyState {
	if c.invocation == nil || c.invocation.Session() == nil {
		return emptyReadonlyState{}
	}
	return c.invocation.Session().State()
}

func (c readonlyInvocationContext) UserID() string {
	if c.invocation == nil || c.invocation.Session() == nil {
		return ""
	}
	return c.invocation.Session().UserID()
}

func (c readonlyInvocationContext) AppName() string {
	if c.invocation == nil || c.invocation.Session() == nil {
		return ""
	}
	return c.invocation.Session().AppName()
}

func (c readonlyInvocationContext) SessionID() string {
	if c.invocation == nil || c.invocation.Session() == nil {
		return ""
	}
	return c.invocation.Session().ID()
}

func (c readonlyInvocationContext) Branch() string {
	if c.invocation == nil {
		return ""
	}
	return c.invocation.Branch()
}

type emptyReadonlyState struct{}

func (emptyReadonlyState) Get(string) (any, error) {
	return nil, session.ErrStateKeyNotExist
}

func (emptyReadonlyState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {}
}

func extractPromptText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		builder.WriteString(part.Text)
	}
	return strings.TrimSpace(builder.String())
}

func contentVisibleText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Text == "" || part.Thought {
			continue
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func convertMCPServers(configs map[string]MCPServerConfig) ([]acp.McpServer, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	servers := make([]acp.McpServer, 0, len(configs))
	keys := make([]string, 0, len(configs))
	for k := range configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		cfg := configs[name]
		svr := acp.McpServer{}
		switch cfg.Type {
		case MCPServerTypeStdio:
			if len(cfg.Cmd) == 0 {
				return nil, fmt.Errorf("mcp server %q: stdio type requires command", name)
			}
			svr.Stdio = &acp.McpServerStdio{
				Name:    name,
				Command: cfg.Cmd[0],
				Env:     envToEnvVars(cfg.Env),
			}
			if len(cfg.Cmd) > 1 {
				svr.Stdio.Args = make([]string, 0, len(cfg.Cmd)-1+len(cfg.Args))
				svr.Stdio.Args = append(svr.Stdio.Args, cfg.Cmd[1:]...)
				svr.Stdio.Args = append(svr.Stdio.Args, cfg.Args...)
			} else {
				// ACP servers like OpenCode reject null for required array fields.
				svr.Stdio.Args = append(make([]string, 0, len(cfg.Args)), cfg.Args...)
			}
		case MCPServerTypeHTTP:
			svr.Http = &acp.McpServerHttpInline{
				Name:    name,
				Type:    "http",
				Url:     cfg.URL,
				Headers: headersToHttpHeaders(cfg.Headers),
			}
		case MCPServerTypeSSE:
			svr.Sse = &acp.McpServerSseInline{
				Name:    name,
				Type:    "sse",
				Url:     cfg.URL,
				Headers: headersToHttpHeaders(cfg.Headers),
			}
		default:
			return nil, fmt.Errorf("unsupported mcp server type %q", cfg.Type)
		}
		servers = append(servers, svr)
	}
	return servers, nil
}

func envToEnvVars(env map[string]string) []acp.EnvVariable {
	vars := make([]acp.EnvVariable, 0, len(env))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vars = append(vars, acp.EnvVariable{Name: k, Value: env[k]})
	}
	return vars
}

func headersToHttpHeaders(headers map[string]string) []acp.HttpHeader {
	h := make([]acp.HttpHeader, 0, len(headers))
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h = append(h, acp.HttpHeader{Name: k, Value: headers[k]})
	}
	return h
}

func mapACPUpdateToEvent(logger zerolog.Logger, invocationID string, ext ExtendedSessionNotification) (*session.Event, bool) {
	update := ext.Update
	switch {
	case update.UserMessageChunk != nil:
		return mapACPUserMessageChunk(logger, invocationID, update.UserMessageChunk)
	case update.AgentMessageChunk != nil:
		return mapACPAgentMessageChunk(logger, invocationID, update.AgentMessageChunk)
	case update.AgentThoughtChunk != nil:
		return mapACPAgentThoughtChunk(logger, invocationID, update.AgentThoughtChunk)
	case update.ToolCall != nil:
		return mapACPToolCall(invocationID, update.ToolCall)
	case update.ToolCallUpdate != nil:
		return mapACPToolCallUpdate(invocationID, update.ToolCallUpdate)
	case update.Plan != nil:
		return mapACPPlanUpdate(logger, invocationID, update.Plan)
	case update.AvailableCommandsUpdate != nil:
		logIgnoredACPUpdate(logger, "available_commands_update", map[string]any{
			"availableCommands": update.AvailableCommandsUpdate.AvailableCommands,
		})
		return nil, false
	case update.CurrentModeUpdate != nil:
		logIgnoredACPUpdate(logger, "current_mode_update", map[string]any{
			"currentModeId": update.CurrentModeUpdate.CurrentModeId,
		})
		return nil, false
	case update.ConfigOptionUpdate != nil:
		logIgnoredACPUpdate(logger, "config_option_update", map[string]any{
			"configOptions": update.ConfigOptionUpdate.ConfigOptions,
		})
		return nil, false
	case update.SessionInfoUpdate != nil:
		logIgnoredACPUpdate(logger, "session_info_update", map[string]any{
			"title":     update.SessionInfoUpdate.Title,
			"updatedAt": update.SessionInfoUpdate.UpdatedAt,
		})
		return nil, false
	case update.UsageUpdate != nil:
		logIgnoredACPUpdate(logger, acpUsageUpdate, map[string]any{
			"size": update.UsageUpdate.Size,
			"used": update.UsageUpdate.Used,
			"cost": update.UsageUpdate.Cost,
		})
		return nil, false
	default:
		// Check for recognized discriminators in raw JSON that are not in the SDK struct.
		var raw map[string]any
		if err := json.Unmarshal(ext.Raw, &raw); err == nil {
			if u, ok := raw["update"].(map[string]any); ok {
				if disc, ok := u["sessionUpdate"].(string); ok && disc == acpUsageUpdate {
					return mapACPLegacyUsageUpdate(logger, invocationID, u)
				}
			}
		}

		logUnsupportedACPUpdate(logger, ext)
		return nil, false
	}
}

func mapACPLegacyUsageUpdate(logger zerolog.Logger, invocationID string, update map[string]any) (*session.Event, bool) {
	usage := mapACPLegacyUsageToUsageMetadata(update)
	if usage == nil {
		logger.Debug().Interface("update", update).Msg("ignoring usage_update with no token counts")
		return nil, false
	}
	ev := session.NewEvent(invocationID)
	ev.UsageMetadata = usage
	ev.Partial = true
	return ev, true
}

func mapACPAgentMessageChunk(logger zerolog.Logger, invocationID string, chunk *acp.SessionUpdateAgentMessageChunk) (*session.Event, bool) {
	part, ok := mapACPContentBlockToPart(logger, chunk.Content)
	if !ok {
		return nil, false
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	ev.Partial = true

	if id, ok := chunk.Meta["messageId"]; ok {
		ev.CustomMetadata = map[string]any{"acp_message_id": id}
	}
	if chunk.MessageId != nil && *chunk.MessageId != "" {
		if ev.CustomMetadata == nil {
			ev.CustomMetadata = map[string]any{}
		}
		ev.CustomMetadata["acp_message_id"] = *chunk.MessageId
	}
	return ev, true
}

func mapACPUserMessageChunk(logger zerolog.Logger, invocationID string, chunk *acp.SessionUpdateUserMessageChunk) (*session.Event, bool) {
	part, ok := mapACPContentBlockToPart(logger, chunk.Content)
	if !ok {
		return nil, false
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleUser)
	ev.Partial = true
	return ev, true
}

func mapACPAgentThoughtChunk(logger zerolog.Logger, invocationID string, chunk *acp.SessionUpdateAgentThoughtChunk) (*session.Event, bool) {
	part, ok := mapACPContentBlockToPart(logger, chunk.Content)
	if !ok {
		return nil, false
	}
	part.Thought = true
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	ev.Partial = true
	return ev, true
}

func mapACPToolCall(invocationID string, tool *acp.SessionUpdateToolCall) (*session.Event, bool) {
	args := map[string]any{
		"kind":      tool.Kind,
		"status":    tool.Status,
		"title":     tool.Title,
		"locations": tool.Locations,
		"rawInput":  tool.RawInput,
		"rawOutput": tool.RawOutput,
	}
	part := &genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   string(tool.ToolCallId),
			Name: "acp_tool_call",
			Args: args,
		},
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	if isACPToolStatusLongRunning(tool.Status) {
		ev.LongRunningToolIDs = []string{string(tool.ToolCallId)}
	}
	return ev, true
}

func mapACPToolCallUpdate(invocationID string, tool *acp.SessionToolCallUpdate) (*session.Event, bool) {
	response := map[string]any{
		"status":    tool.Status,
		"title":     tool.Title,
		"kind":      tool.Kind,
		"locations": tool.Locations,
		"rawInput":  tool.RawInput,
		"rawOutput": tool.RawOutput,
	}
	part := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       string(tool.ToolCallId),
			Name:     "acp_tool_call_update",
			Response: response,
		},
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	if tool.Status != nil && isACPToolStatusLongRunning(*tool.Status) {
		ev.LongRunningToolIDs = []string{string(tool.ToolCallId)}
	}
	return ev, true
}

func mapACPPlanUpdate(_ zerolog.Logger, invocationID string, plan *acp.SessionUpdatePlan) (*session.Event, bool) {
	if plan == nil || len(plan.Entries) == 0 {
		return nil, false
	}
	entries := make([]map[string]any, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		entries = append(entries, map[string]any{
			"content":  entry.Content,
			"status":   entry.Status,
			"priority": entry.Priority,
		})
	}
	ev := session.NewEvent(invocationID)
	ev.Actions.StateDelta[PlanStateKey] = map[string]any{
		acpPlanEntriesKey: entries,
	}
	ev.Partial = true
	return ev, true
}

func mapACPContentBlockToPart(logger zerolog.Logger, block acp.ContentBlock) (*genai.Part, bool) {
	if block.Text != nil {
		if block.Text.Text == "" {
			return nil, false
		}
		return genai.NewPartFromText(block.Text.Text), true
	}
	if block.Image != nil {
		part := mapACPImageToPart(block.Image)
		if part != nil {
			return part, true
		}
	}
	if block.Audio != nil {
		part := mapACPAudioToPart(block.Audio)
		if part != nil {
			return part, true
		}
	}
	if block.ResourceLink != nil {
		part := mapACPResourceLinkToPart(block.ResourceLink)
		if part != nil {
			return part, true
		}
	}
	logIgnoredACPContentBlock(logger, block)
	return nil, false
}

func mapACPImageToPart(img *acp.ContentBlockImage) *genai.Part {
	if img == nil {
		return nil
	}
	mimeType := "image/jpeg"
	if img.MimeType != "" {
		mimeType = img.MimeType
	}
	if img.Data != "" {
		imgBytes, err := decodeBase64(img.Data)
		if err != nil {
			return nil
		}
		return genai.NewPartFromBytes(imgBytes, mimeType)
	}
	if img.Uri != nil && *img.Uri != "" {
		return genai.NewPartFromURI(*img.Uri, mimeType)
	}
	return nil
}

func mapACPAudioToPart(audio *acp.ContentBlockAudio) *genai.Part {
	if audio == nil {
		return nil
	}
	mimeType := "audio/wav"
	if audio.MimeType != "" {
		mimeType = audio.MimeType
	}
	if audio.Data != "" {
		audioBytes, err := decodeBase64(audio.Data)
		if err != nil {
			return nil
		}
		return genai.NewPartFromBytes(audioBytes, mimeType)
	}
	return nil
}

func mapACPResourceLinkToPart(link *acp.ContentBlockResourceLink) *genai.Part {
	if link == nil {
		return nil
	}
	if link.Uri != "" {
		mimeType := "application/octet-stream"
		if link.MimeType != nil && *link.MimeType != "" {
			mimeType = *link.MimeType
		}
		return genai.NewPartFromURI(link.Uri, mimeType)
	}
	return nil
}

func decodeBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty")
	}
	return base64.StdEncoding.DecodeString(s)
}

func marshalACPUpdatePayload(logger zerolog.Logger, payloadType string, v any) (string, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		logger.Debug().
			Err(err).
			Str("acp_payload_type", payloadType).
			Msg("ignoring acp payload that failed to marshal")
		return "", false
	}
	return string(raw), true
}

func isACPToolStatusLongRunning(status acp.ToolCallStatus) bool {
	return status == acp.ToolCallStatusPending || status == acp.ToolCallStatusInProgress
}

func logUnsupportedACPUpdate(logger zerolog.Logger, ext ExtendedSessionNotification) {
	updateType := extendedSessionUpdateType(ext)
	logEvent := logger.Debug().
		Str("acp_update_type", updateType)

	if updateType == unknownValue {
		logEvent = logEvent.RawJSON("acp_update_payload", ext.Raw)
	} else if payload, ok := marshalACPUpdatePayload(logger, "session_update_"+updateType, ext.Update); ok {
		logEvent = logEvent.Str("acp_update_payload", payload)
	}
	logEvent.Msg("ignoring unsupported acp session update")
}

func logIgnoredACPUpdate(logger zerolog.Logger, updateType string, payload any) {
	logEvent := logger.Debug().
		Str("acp_update_type", updateType)

	if marshaled, ok := marshalACPUpdatePayload(logger, "session_update_"+updateType, payload); ok {
		logEvent = logEvent.Str("acp_update_payload", marshaled)
	}
	logEvent.Msg("ignoring non-user-visible acp session update")
}

func extendedSessionUpdateType(ext ExtendedSessionNotification) string {
	if disc := sessionUpdateType(ext.Update); disc != unknownValue {
		return disc
	}

	var raw map[string]any
	if err := json.Unmarshal(ext.Raw, &raw); err == nil {
		if u, ok := raw["update"].(map[string]any); ok {
			if disc, ok := u["sessionUpdate"].(string); ok {
				return disc
			}
		}
	}
	return unknownValue
}

func logIgnoredACPContentBlock(logger zerolog.Logger, block acp.ContentBlock) {
	blockType := contentBlockType(block)
	logEvent := logger.Debug().
		Str("acp_content_block_type", blockType).
		Str("acp_content_block_text", acpContentBlockLogText(block)).
		Interface("acp_content_block", acpContentBlockLogValue(block))

	if blockType == unknownValue {
		logEvent.Msg("ignoring unsupported acp content block")
		return
	}
	logEvent.Msg("ignoring non-text acp content block")
}

func acpContentBlockLogText(block acp.ContentBlock) string {
	switch {
	case block.Text != nil:
		return strings.TrimSpace(block.Text.Text)
	case block.Image != nil:
		return acpTypeImage
	case block.Audio != nil:
		return acpTypeAudio
	case block.ResourceLink != nil:
		return fmt.Sprintf("resource_link name=%q uri=%q", block.ResourceLink.Name, block.ResourceLink.Uri)
	case block.Resource != nil:
		return acpTypeResource
	default:
		return unknownValue
	}
}

func acpContentBlockLogValue(block acp.ContentBlock) map[string]any {
	switch {
	case block.Text != nil:
		return map[string]any{
			"type": acpTypeText,
			"text": block.Text.Text,
		}
	case block.Image != nil:
		return logACPImageBlockValue(block.Image)
	case block.Audio != nil:
		return logACPAudioBlockValue(block.Audio)
	case block.ResourceLink != nil:
		return logACPResourceLinkBlockValue(block.ResourceLink)
	case block.Resource != nil:
		return map[string]any{
			"type":     acpTypeResource,
			"resource": acpEmbeddedResourceLogValue(block.Resource.Resource),
		}
	default:
		return map[string]any{"type": unknownValue}
	}
}

func logACPImageBlockValue(img *acp.ContentBlockImage) map[string]any {
	obj := map[string]any{"type": acpTypeImage}
	if img.MimeType != "" {
		obj["mime_type"] = img.MimeType
	}
	if img.Uri != nil && *img.Uri != "" {
		obj["uri"] = *img.Uri
	}
	if img.Data != "" {
		obj["data_len"] = len(img.Data)
	}
	return obj
}

func logACPAudioBlockValue(audio *acp.ContentBlockAudio) map[string]any {
	obj := map[string]any{"type": acpTypeAudio}
	if audio.MimeType != "" {
		obj["mime_type"] = audio.MimeType
	}
	if audio.Data != "" {
		obj["data_len"] = len(audio.Data)
	}
	return obj
}

func logACPResourceLinkBlockValue(link *acp.ContentBlockResourceLink) map[string]any {
	obj := map[string]any{"type": "resource_link"}
	if link.Name != "" {
		obj["name"] = link.Name
	}
	if link.Uri != "" {
		obj["uri"] = link.Uri
	}
	if link.Description != nil && *link.Description != "" {
		obj["description"] = *link.Description
	}
	if link.MimeType != nil && *link.MimeType != "" {
		obj["mime_type"] = *link.MimeType
	}
	if link.Size != nil {
		obj["size"] = *link.Size
	}
	if link.Title != nil && *link.Title != "" {
		obj["title"] = *link.Title
	}
	return obj
}

func acpEmbeddedResourceLogValue(resource acp.EmbeddedResourceResource) map[string]any {
	switch {
	case resource.TextResourceContents != nil:
		return logACPTextResourceValue(resource.TextResourceContents)
	case resource.BlobResourceContents != nil:
		return logACPBlobResourceValue(resource.BlobResourceContents)
	default:
		return map[string]any{"kind": unknownValue}
	}
}

func logACPTextResourceValue(res *acp.TextResourceContents) map[string]any {
	obj := map[string]any{"kind": acpTypeText}
	if res.Uri != "" {
		obj["uri"] = res.Uri
	}
	if res.MimeType != nil && *res.MimeType != "" {
		obj["mime_type"] = *res.MimeType
	}
	if res.Text != "" {
		obj["text_len"] = len(res.Text)
	}
	return obj
}

func logACPBlobResourceValue(res *acp.BlobResourceContents) map[string]any {
	obj := map[string]any{"kind": "blob"}
	if res.Uri != "" {
		obj["uri"] = res.Uri
	}
	if res.MimeType != nil && *res.MimeType != "" {
		obj["mime_type"] = *res.MimeType
	}
	if res.Blob != "" {
		obj["blob_len"] = len(res.Blob)
	}
	return obj
}

func sessionUpdateType(update acp.SessionUpdate) string {
	switch {
	case update.UserMessageChunk != nil:
		return "user_message_chunk"
	case update.AgentMessageChunk != nil:
		return "agent_message_chunk"
	case update.AgentThoughtChunk != nil:
		return "agent_thought_chunk"
	case update.ToolCall != nil:
		return "tool_call"
	case update.ToolCallUpdate != nil:
		return "tool_call_update"
	case update.Plan != nil:
		return "plan"
	case update.CurrentModeUpdate != nil:
		return "current_mode_update"
	case update.AvailableCommandsUpdate != nil:
		return "available_commands_update"
	case update.ConfigOptionUpdate != nil:
		return "config_option_update"
	case update.SessionInfoUpdate != nil:
		return "session_info_update"
	case update.UsageUpdate != nil:
		return acpUsageUpdate
	default:
		return unknownValue
	}
}

func contentBlockType(block acp.ContentBlock) string {
	switch {
	case block.Text != nil:
		return acpTypeText
	case block.Image != nil:
		return acpTypeImage
	case block.Audio != nil:
		return acpTypeAudio
	case block.ResourceLink != nil:
		return "resource_link"
	case block.Resource != nil:
		return acpTypeResource
	default:
		return unknownValue
	}
}
