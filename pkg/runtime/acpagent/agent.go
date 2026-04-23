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
	"sort"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Config configures an ACP-backed ADK agent.
type Config struct {
	// Context is the base context for the agent's lifecycle.
	Context context.Context
	// Name is the display name of the agent. Defaults to "ACPAgent".
	Name string
	// Description describes the agent's purpose.
	Description string
	// Model is the specific LLM model identifier to use.
	Model string
	// Mode is the ACP session mode identifier to use.
	Mode string
	// SystemInstructions is an optional system-level instruction for the agent.
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
	//     via session state key [SessionStateKey].cwd.
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
	// SessionID is an optional desired session ID to request when creating ACP sessions.
	// It is sent via session/new _meta.sessionId and may be ignored by some ACP runtimes.
	SessionID string
}

// Agent adapts an Agentic Computing Protocol (ACP) runtime to the ADK agent interface.
// It manages the lifecycle of an ACP subprocess and maps ACP sessions to ADK sessions.
type Agent struct {
	adkagent.Agent

	client             *Client
	workingDir         string
	sessionModel       string
	sessionMode        string
	sessionID          string
	systemInstructions string
	logger             zerolog.Logger
	sessionMu          sync.Mutex
	bindingByADK       map[string]acpSessionBinding
	mcpServers         []acp.McpServer
}

type acpSessionBinding struct {
	remoteSessionID string
	cwd             string
	metaJSON        string
}

type acpSessionConfig struct {
	cwd      string
	meta     map[string]any
	metaJSON string
}

const (
	defaultAgentName        = "ACPAgent"
	defaultAgentDescription = "ACP runtime exposed through ADK"

	acpTypeText     = "text"
	acpTypeImage    = "image"
	acpTypeAudio    = "audio"
	acpTypeResource = "resource"
)

// SessionStateKey is the reserved ADK session-state key for ACP per-session
// overrides in this package.
//
// The value at this key must be an object with optional fields:
//   - "cwd" (string): overrides ACP session/new cwd
//   - "meta" (object): forwarded to ACP session/new._meta
//
// Set it before the first invocation in a given ADK session; once that ADK
// session is bound to an ACP session, later changes do not rebind.
const SessionStateKey = "acp_session"

var _ adkagent.Agent = (*Agent)(nil)

// New creates an ADK agent backed by an ACP client process.
//
// It starts the ACP process, performs ACP initialization, and creates ACP
// sessions lazily per ADK session.
//
// Per ADK session, callers may provide session-state overrides using key
// [SessionStateKey] with optional fields:
//   - "cwd" (string): override ACP session/new cwd
//   - "meta" (object): forwarded to ACP session/new._meta
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
		client:             client,
		workingDir:         cfg.WorkingDir,
		sessionModel:       strings.TrimSpace(cfg.Model),
		sessionMode:        strings.TrimSpace(cfg.Mode),
		sessionID:          strings.TrimSpace(cfg.SessionID),
		systemInstructions: strings.TrimSpace(cfg.SystemInstructions),
		logger:             l,
		bindingByADK:       make(map[string]acpSessionBinding),
		mcpServers:         mcpServers,
	}
	base, err := adkagent.New(adkagent.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
		Run:         a.run,
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

		prompt := extractPromptText(ctx.UserContent())
		if a.systemInstructions != "" {
			prompt = a.systemInstructions + "\n\n" + prompt
		}

		if strings.TrimSpace(prompt) == "" {
			yield(nil, errors.New("prompt is empty"))
			return
		}

		remoteSessionID, err := a.ensureRemoteSession(ctx, logger, ctx.Session().ID())
		if err != nil {
			yield(nil, err)
			return
		}

		logger.Debug().
			Str("adk_session_id", ctx.Session().ID()).
			Str("acp_session_id", remoteSessionID).
			Int("prompt_len", len(prompt)).
			Msg("starting adk invocation")

		updates, resultCh, err := a.client.Prompt(ctx, remoteSessionID, prompt)
		if err != nil {
			yield(nil, err)
			return
		}

		var promptResult *PromptResult
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
			Str("adk_session_id", ctx.Session().ID()).
			Str("acp_session_id", remoteSessionID).
			Msg("completed adk invocation")

		ev := session.NewEvent(ctx.InvocationID())
		if promptResult != nil {
			ev.FinishReason = mapACPStopReasonToFinishReason(promptResult.Response.StopReason)
			ev.UsageMetadata = mapACPUsageToUsageMetadata(promptResult.Usage)
		}
		ev.TurnComplete = true
		a.logADKEvent(logger, ev, "yielding final turn complete event")
		if !yield(ev, nil) {
			return
		}
	}
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
	case acp.StopReasonCancelled:
		return genai.FinishReasonOther // No direct match for cancelled in genai.FinishReason
	default:
		return genai.FinishReasonUnspecified
	}
}

func mapACPUsageToUsageMetadata(usage map[string]any) *genai.GenerateContentResponseUsageMetadata {
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
		if binding.cwd != cfg.cwd || binding.metaJSON != cfg.metaJSON {
			logger.Warn().
				Str("adk_session_id", adkSessionID).
				Str("acp_session_id", binding.remoteSessionID).
				Str("bound_cwd", binding.cwd).
				Str("requested_cwd", cfg.cwd).
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

	resp, err := a.client.CreateSessionWithMeta(ctx, cfg.cwd, a.sessionModel, a.sessionMode, a.mcpServers, cfg.meta)
	if err != nil {
		return "", err
	}
	sessionID := string(resp.SessionId)
	a.bindingByADK[adkSessionID] = acpSessionBinding{
		remoteSessionID: sessionID,
		cwd:             cfg.cwd,
		metaJSON:        cfg.metaJSON,
	}
	event := logger.Debug().
		Str("adk_session_id", adkSessionID).
		Str("acp_session_id", sessionID).
		Str("cwd", cfg.cwd).
		RawJSON("meta", []byte(cfg.metaJSON))
	if a.sessionModel != "" {
		event = event.Str("model", a.sessionModel)
	}
	if a.sessionMode != "" {
		event = event.Str("mode", a.sessionMode)
	}
	event.Msg("created new acp session for adk session")
	return sessionID, nil
}

func (a *Agent) resolveSessionConfig(ctx adkagent.InvocationContext) (acpSessionConfig, error) {
	cfg := acpSessionConfig{
		cwd: strings.TrimSpace(a.workingDir),
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
	if rawCWD, ok := state["cwd"]; ok {
		cwd, ok := rawCWD.(string)
		if !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q.cwd must be a string; got %T", SessionStateKey, rawCWD)
		}
		cfg.cwd = strings.TrimSpace(cwd)
	}
	if rawMeta, ok := state["meta"]; ok {
		meta, ok := rawMeta.(map[string]any)
		if !ok {
			return acpSessionConfig{}, fmt.Errorf("adk session state %q.meta must be an object; got %T", SessionStateKey, rawMeta)
		}
		cfg.meta = cloneAnyMap(meta)
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
			svr.Http = &acp.McpServerHttp{
				Name:    name,
				Url:     cfg.URL,
				Headers: headersToHttpHeaders(cfg.Headers),
			}
		case MCPServerTypeSSE:
			svr.Sse = &acp.McpServerSse{
				Name:    name,
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
	default:
		// Check for recognized discriminators in raw JSON that are not in the SDK struct.
		var raw map[string]any
		if err := json.Unmarshal(ext.Raw, &raw); err == nil {
			if u, ok := raw["update"].(map[string]any); ok {
				if disc, ok := u["sessionUpdate"].(string); ok && disc == "usage_update" {
					return mapACPUsageUpdate(logger, invocationID, u)
				}
			}
		}

		logUnsupportedACPUpdate(logger, ext)
		return nil, false
	}
}

func mapACPUsageUpdate(logger zerolog.Logger, invocationID string, update map[string]any) (*session.Event, bool) {
	usage := mapACPUsageToUsageMetadata(update)
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

	if meta, ok := chunk.Meta.(map[string]any); ok {
		if id, ok := meta["messageId"]; ok {
			ev.CustomMetadata = map[string]any{"acp_message_id": id}
		}
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

func mapACPPlanUpdate(logger zerolog.Logger, invocationID string, plan *acp.SessionUpdatePlan) (*session.Event, bool) {
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
	part := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:   "acp_plan",
			Name: "acp_plan_update",
			Response: map[string]any{
				"entries": entries,
			},
		},
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
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
