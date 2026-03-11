package codexacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"
	normalogging "github.com/metalagman/norma/internal/logging"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

// DefaultAgentName is the default ACP agent name reported by the proxy.
const DefaultAgentName = "norma-codex-acp-proxy"

var (
	validCodexApprovalPolicies = map[string]struct{}{
		"untrusted":  {},
		"on-failure": {},
		"on-request": {},
		"never":      {},
	}
	validCodexSandboxModes = map[string]struct{}{
		"read-only":          {},
		"workspace-write":    {},
		"danger-full-access": {},
	}
)

// Options configures Codex MCP -> ACP proxy behavior.
type Options struct {
	Name string

	CodexApprovalPolicy        string
	CodexBaseInstructions      string
	CodexCompactPrompt         string
	CodexConfig                map[string]any
	CodexDeveloperInstructions string
	CodexModel                 string
	CodexProfile               string
	CodexSandbox               string
}

type codexACPProxyAgent struct {
	mcpSession         codexMCPToolSession
	agentName          string
	defaultCodexConfig codexToolConfig
	logger             zerolog.Logger

	connMu sync.RWMutex
	conn   codexACPSessionUpdater

	mu            sync.Mutex
	sessions      map[acp.SessionId]*codexProxySessionState
	nextSessionID uint64
	nextToolID    uint64
}

type codexProxySessionState struct {
	cwd    string
	thread string
	model  string
	cancel context.CancelFunc
}

type codexToolConfig struct {
	ApprovalPolicy        string
	BaseInstructions      string
	CompactPrompt         string
	Config                map[string]any
	DeveloperInstructions string
	Model                 string
	Profile               string
	Sandbox               string
}

func (c codexToolConfig) withModel(model string) codexToolConfig {
	next := c
	nextModel := strings.TrimSpace(model)
	if nextModel != "" {
		next.Model = nextModel
	}
	return next
}

func (c codexToolConfig) applyTo(args map[string]any) {
	if args == nil {
		return
	}
	if approval := strings.TrimSpace(c.ApprovalPolicy); approval != "" {
		args["approval-policy"] = approval
	}
	if base := strings.TrimSpace(c.BaseInstructions); base != "" {
		args["base-instructions"] = base
	}
	if compact := strings.TrimSpace(c.CompactPrompt); compact != "" {
		args["compact-prompt"] = compact
	}
	if cfg := cloneMap(c.Config); len(cfg) > 0 {
		args["config"] = cfg
	}
	if developer := strings.TrimSpace(c.DeveloperInstructions); developer != "" {
		args["developer-instructions"] = developer
	}
	if model := strings.TrimSpace(c.Model); model != "" {
		args["model"] = model
	}
	if profile := strings.TrimSpace(c.Profile); profile != "" {
		args["profile"] = profile
	}
	if sandbox := strings.TrimSpace(c.Sandbox); sandbox != "" {
		args["sandbox"] = sandbox
	}
}

func (o Options) codexToolConfig() codexToolConfig {
	return codexToolConfig{
		ApprovalPolicy:        strings.TrimSpace(o.CodexApprovalPolicy),
		BaseInstructions:      strings.TrimSpace(o.CodexBaseInstructions),
		CompactPrompt:         strings.TrimSpace(o.CodexCompactPrompt),
		Config:                cloneMap(o.CodexConfig),
		DeveloperInstructions: strings.TrimSpace(o.CodexDeveloperInstructions),
		Model:                 strings.TrimSpace(o.CodexModel),
		Profile:               strings.TrimSpace(o.CodexProfile),
		Sandbox:               strings.TrimSpace(o.CodexSandbox),
	}
}

func (o Options) validate() error {
	if err := validateEnumValue("codex approval policy", o.CodexApprovalPolicy, validCodexApprovalPolicies); err != nil {
		return err
	}
	if err := validateEnumValue("codex sandbox", o.CodexSandbox, validCodexSandboxModes); err != nil {
		return err
	}
	return nil
}

func validateEnumValue(label string, value string, allowed map[string]struct{}) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if _, ok := allowed[trimmed]; ok {
		return nil
	}
	return fmt.Errorf("invalid %s %q", label, trimmed)
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

type codexMCPToolSession interface {
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
	ListTools(ctx context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	Close() error
	Wait() error
}

type codexACPSessionUpdater interface {
	SessionUpdate(ctx context.Context, params acp.SessionNotification) error
}

type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string {
	if e == nil || e.err == nil {
		return "command exited with error"
	}
	return e.err.Error()
}

func (e *exitCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *exitCodeError) ExitCode() int {
	if e == nil || e.code <= 0 {
		return 1
	}
	return e.code
}

// RunProxy starts a Codex MCP server and exposes it as an ACP agent over stdio.
func RunProxy(ctx context.Context, repoRoot string, opts Options, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := opts.validate(); err != nil {
		return err
	}
	lockedStderr := &syncWriter{writer: stderr}
	logLevel := zerolog.InfoLevel
	if normalogging.DebugEnabled() {
		logLevel = zerolog.DebugLevel
	}
	logger := zerolog.New(zerolog.ConsoleWriter{
		Out:        lockedStderr,
		TimeFormat: time.RFC3339,
	}).
		Level(logLevel).
		With().
		Timestamp().
		Str("component", "codex.acp.proxy").
		Logger()

	command := buildCodexMCPCommand(opts)
	agentName := strings.TrimSpace(opts.Name)
	if agentName == "" {
		agentName = DefaultAgentName
	}
	logger.Debug().
		Str("repo_root", repoRoot).
		Str("agent_name", agentName).
		Strs("command", command).
		Msg("starting codex acp proxy")

	mcpSession, err := connectCodexMCPProxySession(ctx, repoRoot, command, agentName, lockedStderr, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to connect codex mcp session")
		return err
	}
	defer func() {
		logger.Debug().Msg("closing codex mcp session")
		_ = mcpSession.Close()
	}()

	if err := ensureCodexProxyTools(ctx, mcpSession, logger); err != nil {
		logger.Error().Err(err).Msg("required codex tools validation failed")
		return err
	}

	proxy := newCodexACPProxyAgent(mcpSession, agentName, opts.codexToolConfig(), logger)
	conn := acp.NewAgentSideConnection(proxy, stdout, stdin)
	proxy.setConnection(conn)
	logger.Debug().Msg("acp connection initialized")

	backendDone := make(chan error, 1)
	go func() {
		backendDone <- mcpSession.Wait()
	}()

	select {
	case <-conn.Done():
		logger.Debug().Msg("acp client disconnected")
		_ = mcpSession.Close()
		_ = awaitBackendStop(backendDone, 2*time.Second)
		return nil
	case err := <-backendDone:
		if err == nil {
			logger.Error().Msg("codex mcp-server exited before acp client disconnected")
			return &exitCodeError{
				code: 1,
				err:  errors.New("codex mcp-server exited before ACP client disconnected"),
			}
		}
		code := extractExitCode(err)
		logger.Error().Err(err).Int("exit_code", code).Msg("codex mcp-server exited")
		return &exitCodeError{
			code: code,
			err:  fmt.Errorf("codex mcp-server exited: %w", err),
		}
	case <-ctx.Done():
		logger.Warn().Err(ctx.Err()).Msg("proxy context canceled")
		_ = mcpSession.Close()
		_ = awaitBackendStop(backendDone, 2*time.Second)
		return ctx.Err()
	}
}

func buildCodexMCPCommand(opts Options) []string {
	command := make([]string, 0, 2)
	command = append(command, "codex", "mcp-server")
	return command
}

func connectCodexMCPProxySession(ctx context.Context, repoRoot string, command []string, agentName string, stderr io.Writer, logger zerolog.Logger) (codexMCPToolSession, error) {
	if len(command) == 0 {
		return nil, errors.New("empty codex command")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: agentName, Version: "v0.0.1"}, nil)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Stderr = stderr
	logger.Debug().Str("cwd", repoRoot).Strs("command", command).Msg("connecting mcp command transport")
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to mcp command: %w", err)
	}
	logger.Debug().Msg("connected to codex mcp session")
	return session, nil
}

func ensureCodexProxyTools(ctx context.Context, session codexMCPToolSession, logger zerolog.Logger) error {
	logger.Debug().
		Str("proto", "mcp").
		Str("method", "tools/list").
		Str("phase", "request").
		Msg("mcp event")
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		logger.Error().
			Str("proto", "mcp").
			Str("method", "tools/list").
			Str("phase", "error").
			Err(err).
			Msg("mcp event")
		return fmt.Errorf("list mcp tools: %w", err)
	}
	if toolsResult == nil || len(toolsResult.Tools) == 0 {
		return errors.New("mcp tools list is empty")
	}
	toolNames := make([]string, 0, len(toolsResult.Tools))
	for _, t := range toolsResult.Tools {
		if t == nil {
			continue
		}
		toolNames = append(toolNames, t.Name)
	}
	logger.Debug().
		Str("proto", "mcp").
		Str("method", "tools/list").
		Str("phase", "response").
		Int("tool_count", len(toolNames)).
		Strs("tools", toolNames).
		Str("payload", logJSON(toolsResult)).
		Msg("mcp event")
	seen := map[string]bool{}
	for _, t := range toolsResult.Tools {
		if t == nil {
			continue
		}
		seen[t.Name] = true
	}
	if !seen["codex"] || !seen["codex-reply"] {
		return fmt.Errorf("required tools not found (codex=%t codex-reply=%t)", seen["codex"], seen["codex-reply"])
	}
	logger.Debug().Msg("required codex tools are available")
	return nil
}

func newCodexACPProxyAgent(mcpSession codexMCPToolSession, agentName string, defaultCodexConfig codexToolConfig, logger zerolog.Logger) *codexACPProxyAgent {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = DefaultAgentName
	}
	return &codexACPProxyAgent{
		mcpSession:         mcpSession,
		agentName:          name,
		defaultCodexConfig: defaultCodexConfig.withModel(defaultCodexConfig.Model),
		logger:             logger.With().Str("agent_name", name).Logger(),
		sessions:           make(map[acp.SessionId]*codexProxySessionState),
	}
}

func (a *codexACPProxyAgent) setConnection(conn codexACPSessionUpdater) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.conn = conn
}

func (a *codexACPProxyAgent) Authenticate(_ context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	a.logger.Debug().Msg("received authenticate")
	return acp.AuthenticateResponse{}, nil
}

func (a *codexACPProxyAgent) Initialize(_ context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	_ = params
	a.logger.Debug().Msg("received initialize")
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    a.agentName,
			Version: "dev",
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: false,
			PromptCapabilities: acp.PromptCapabilities{
				Audio:           false,
				Image:           false,
				EmbeddedContext: false,
			},
		},
		AuthMethods: []acp.AuthMethod{},
	}, nil
}

func (a *codexACPProxyAgent) Cancel(_ context.Context, params acp.CancelNotification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.sessions[params.SessionId]
	if !ok || state.cancel == nil {
		a.logger.Debug().Str("session_id", string(params.SessionId)).Msg("received cancel for inactive session")
		return nil
	}
	a.logger.Debug().Str("session_id", string(params.SessionId)).Msg("canceling active prompt")
	state.cancel()
	return nil
}

func (a *codexACPProxyAgent) NewSession(_ context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionID := acp.SessionId(fmt.Sprintf("session-%d", atomic.AddUint64(&a.nextSessionID, 1)))

	a.mu.Lock()
	a.sessions[sessionID] = &codexProxySessionState{
		cwd:   strings.TrimSpace(params.Cwd),
		model: a.defaultCodexConfig.Model,
	}
	a.mu.Unlock()
	a.logger.Debug().
		Str("session_id", string(sessionID)).
		Str("cwd", strings.TrimSpace(params.Cwd)).
		Msg("created session")

	resp := acp.NewSessionResponse{SessionId: sessionID}
	if a.defaultCodexConfig.Model != "" {
		resp.Models = &acp.SessionModelState{
			CurrentModelId: acp.ModelId(a.defaultCodexConfig.Model),
			AvailableModels: []acp.ModelInfo{
				{ModelId: acp.ModelId(a.defaultCodexConfig.Model), Name: a.defaultCodexConfig.Model},
			},
		}
	}
	return resp, nil
}

func (a *codexACPProxyAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	userPrompt := strings.TrimSpace(joinPromptText(params.Prompt))
	if userPrompt == "" {
		return acp.PromptResponse{}, acp.NewInvalidParams("prompt must include at least one text block")
	}
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Int("prompt_len", len(userPrompt)).
		Msg("received prompt")

	a.mu.Lock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidParams("session not found")
	}
	if state.cancel != nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("prompt already active for session")
	}
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	threadID := state.thread
	cwd := state.cwd
	model := state.model
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if cur, exists := a.sessions[params.SessionId]; exists {
			cur.cancel = nil
		}
		a.mu.Unlock()
	}()

	toolID := acp.ToolCallId(fmt.Sprintf("codex-tool-%d", atomic.AddUint64(&a.nextToolID, 1)))
	toolName, toolArgs := buildCodexToolInvocation(threadID, cwd, userPrompt, a.defaultCodexConfig, model)
	callStartedAt := time.Now()
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("tool_name", toolName).
		Str("tool_id", string(toolID)).
		Str("tool_args", logJSON(toolArgs)).
		Str("proto", "mcp").
		Str("method", "tools/call").
		Str("phase", "request").
		Msg("invoking mcp tool")
	if err := a.sendUpdate(promptCtx, params.SessionId, acp.StartToolCall(
		toolID,
		toolName,
		acp.WithStartKind(acp.ToolKindExecute),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
		acp.WithStartRawInput(toolArgs),
	)); err != nil {
		return acp.PromptResponse{}, err
	}

	result, err := a.mcpSession.CallTool(promptCtx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: toolArgs,
	})
	if err != nil {
		status := acp.ToolCallStatusFailed
		if errors.Is(promptCtx.Err(), context.Canceled) {
			status = acp.ToolCallStatusCompleted
		}
		_ = a.sendUpdate(context.Background(), params.SessionId, acp.UpdateToolCall(
			toolID,
			acp.WithUpdateStatus(status),
			acp.WithUpdateRawOutput(map[string]any{"error": err.Error()}),
		))
		if errors.Is(promptCtx.Err(), context.Canceled) {
			a.logger.Debug().Str("session_id", string(params.SessionId)).Msg("prompt canceled during tool call")
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		a.logger.Error().
			Err(err).
			Str("session_id", string(params.SessionId)).
			Str("tool_name", toolName).
			Str("proto", "mcp").
			Str("method", "tools/call").
			Str("phase", "error").
			Dur("duration", time.Since(callStartedAt)).
			Msg("mcp event")
		return acp.PromptResponse{}, fmt.Errorf("call mcp tool %q: %w", toolName, err)
	}
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("tool_name", toolName).
		Str("proto", "mcp").
		Str("method", "tools/call").
		Str("phase", "response").
		Dur("duration", time.Since(callStartedAt)).
		Bool("is_error", result != nil && result.IsError).
		Str("result_payload", logJSON(result)).
		Msg("mcp event")

	thread, responseText := extractCodexToolResult(result)
	if thread != "" {
		a.mu.Lock()
		if cur, exists := a.sessions[params.SessionId]; exists {
			cur.thread = thread
		}
		a.mu.Unlock()
	}

	callStatus := acp.ToolCallStatusCompleted
	if result != nil && result.IsError {
		callStatus = acp.ToolCallStatusFailed
	}
	if err := a.sendUpdate(promptCtx, params.SessionId, acp.UpdateToolCall(
		toolID,
		acp.WithUpdateStatus(callStatus),
		acp.WithUpdateRawOutput(result),
	)); err != nil {
		return acp.PromptResponse{}, err
	}

	if strings.TrimSpace(responseText) != "" {
		if err := a.sendUpdate(promptCtx, params.SessionId, acp.UpdateAgentMessageText(responseText)); err != nil {
			return acp.PromptResponse{}, err
		}
	}

	if errors.Is(promptCtx.Err(), context.Canceled) {
		a.logger.Debug().Str("session_id", string(params.SessionId)).Msg("prompt completed as canceled")
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Int("response_len", len(responseText)).
		Bool("tool_error", result != nil && result.IsError).
		Msg("prompt completed")
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *codexACPProxyAgent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	_ = params
	a.logger.Debug().Msg("received set_session_mode")
	return acp.SetSessionModeResponse{}, nil
}

func (a *codexACPProxyAgent) SetSessionModel(_ context.Context, params acp.SetSessionModelRequest) (acp.SetSessionModelResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		return acp.SetSessionModelResponse{}, acp.NewInvalidParams("session not found")
	}
	state.model = strings.TrimSpace(string(params.ModelId))
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("model", state.model).
		Msg("received set_session_model")
	return acp.SetSessionModelResponse{}, nil
}

func (a *codexACPProxyAgent) sendUpdate(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) error {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return errors.New("acp connection is not initialized")
	}
	a.logger.Debug().
		Str("proto", "acp").
		Str("session_id", string(sessionID)).
		Str("update_type", sessionUpdateType(update)).
		Str("update_payload", sessionUpdatePayload(update)).
		Msg("sending session update")
	return conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    update,
	})
}

func sessionUpdateType(update acp.SessionUpdate) string {
	switch {
	case update.UserMessageChunk != nil:
		return "user_message_chunk"
	case update.ToolCall != nil:
		return "tool_call"
	case update.ToolCallUpdate != nil:
		return "tool_call_update"
	case update.AgentMessageChunk != nil:
		return "agent_message_chunk"
	case update.AgentThoughtChunk != nil:
		return "agent_thought_chunk"
	case update.Plan != nil:
		return "plan"
	case update.AvailableCommandsUpdate != nil:
		return "available_commands_update"
	case update.CurrentModeUpdate != nil:
		return "current_mode_update"
	default:
		return "unknown"
	}
}

func sessionUpdatePayload(update acp.SessionUpdate) string {
	raw, err := json.Marshal(update)
	if err != nil {
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	}
	return string(raw)
}

func logJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	}
	const maxPayloadLen = 4096
	if len(raw) <= maxPayloadLen {
		return string(raw)
	}
	return string(raw[:maxPayloadLen]) + fmt.Sprintf(`...{"truncated_bytes":%d}`, len(raw)-maxPayloadLen)
}

func buildCodexToolInvocation(threadID, cwd, prompt string, defaultConfig codexToolConfig, sessionModel string) (string, map[string]any) {
	args := map[string]any{
		"prompt": prompt,
	}
	trimmedCwd := strings.TrimSpace(cwd)
	if trimmedCwd != "" && threadID == "" {
		args["cwd"] = trimmedCwd
	}
	if strings.TrimSpace(threadID) == "" {
		defaultConfig.withModel(sessionModel).applyTo(args)
		return "codex", args
	}
	args["threadId"] = strings.TrimSpace(threadID)
	return "codex-reply", args
}

func joinPromptText(blocks []acp.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, block := range blocks {
		if block.Text == nil {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(block.Text.Text)
	}
	return builder.String()
}

func extractCodexToolResult(result *mcp.CallToolResult) (threadID string, text string) {
	if result == nil {
		return "", ""
	}

	structuredContent := any(nil)
	structuredText := ""

	switch payload := result.StructuredContent.(type) {
	case map[string]any:
		structuredContent = payload
		if thread, ok := payload["threadId"].(string); ok {
			threadID = strings.TrimSpace(thread)
		}
		if contentText, ok := payload["content"].(string); ok {
			structuredText = strings.TrimSpace(contentText)
		}
	default:
		structuredContent = payload
	}

	if structuredText != "" {
		return threadID, structuredText
	}

	textParts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		textContent, ok := item.(*mcp.TextContent)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(textContent.Text)
		if trimmed == "" {
			continue
		}
		textParts = append(textParts, trimmed)
	}
	if len(textParts) > 0 {
		return threadID, strings.Join(textParts, "\n")
	}

	if structuredContent != nil {
		raw, err := json.Marshal(structuredContent)
		if err == nil && len(raw) > 0 {
			return threadID, string(raw)
		}
	}
	return threadID, ""
}

func awaitBackendStop(ch <-chan error, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return errors.New("timeout waiting for codex mcp-server shutdown")
	}
}

func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
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
