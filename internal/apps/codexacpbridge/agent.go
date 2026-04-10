package codexacpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

type codexACPProxyAgent struct {
	agentName          string
	agentVersion       string
	defaultCodexConfig codexToolConfig
	sessionFactory     codexMCPToolSessionFactory
	logger             *zerolog.Logger

	connMu sync.RWMutex
	conn   codexACPSessionUpdater

	mu            sync.Mutex
	sessions      map[acp.SessionId]*codexProxySessionState
	nextSessionID uint64
	nextToolID    uint64
}

type codexProxySessionState struct {
	cwd        string
	thread     string
	model      string
	mode       string
	mcpServers map[string]acp.McpServer
	reason     string
	backend    codexMCPToolSession
	cancel     context.CancelFunc
}

type codexACPSessionUpdater interface {
	SessionUpdate(ctx context.Context, params acp.SessionNotification) error
}

func newCodexACPProxyAgent(mcpSession codexMCPToolSession, agentName string, defaultCodexConfig codexToolConfig, logger *zerolog.Logger) *codexACPProxyAgent {
	return newCodexACPProxyAgentWithFactory(
		func(context.Context, string) (codexMCPToolSession, error) { return mcpSession, nil },
		agentName,
		defaultCodexConfig,
		logger,
	)
}

func newCodexACPProxyAgentWithFactory(
	sessionFactory codexMCPToolSessionFactory,
	agentName string,
	defaultCodexConfig codexToolConfig,
	logger *zerolog.Logger,
) *codexACPProxyAgent {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = DefaultAgentName
	}
	version := DefaultAgentVersion
	return &codexACPProxyAgent{
		agentName:          name,
		agentVersion:       version,
		defaultCodexConfig: defaultCodexConfig.withModel(defaultCodexConfig.Model),
		sessionFactory:     sessionFactory,
		logger:             logger,
		sessions:           make(map[acp.SessionId]*codexProxySessionState),
	}
}

func (a *codexACPProxyAgent) setConnection(conn codexACPSessionUpdater) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.conn = conn
}

func (a *codexACPProxyAgent) setAgentVersion(version string) {
	next := strings.TrimSpace(version)
	if next == "" {
		next = DefaultAgentVersion
	}
	a.agentVersion = next
}

func (a *codexACPProxyAgent) Authenticate(_ context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	a.logger.Debug().Msg("received authenticate")
	return acp.AuthenticateResponse{}, nil
}

func (a *codexACPProxyAgent) Initialize(_ context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	a.logger.Debug().Msg("received initialize")
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    a.agentName,
			Version: a.agentVersion,
		},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: false,
			McpCapabilities: acp.McpCapabilities{
				Http: true,
				Sse:  false,
			},
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

func (a *codexACPProxyAgent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionID := acp.SessionId(fmt.Sprintf("session-%d", atomic.AddUint64(&a.nextSessionID, 1)))

	var mcpServers map[string]acp.McpServer
	if len(params.McpServers) > 0 {
		var err error
		mcpServers, err = validateMCPServers(params.McpServers)
		if err != nil {
			return acp.NewSessionResponse{}, acp.NewInvalidParams(err.Error())
		}
	}

	a.mu.Lock()
	a.sessions[sessionID] = &codexProxySessionState{
		cwd:        strings.TrimSpace(params.Cwd),
		model:      a.defaultCodexConfig.Model,
		mcpServers: mcpServers,
		reason:     backendRestartReasonSessionNew,
	}
	a.mu.Unlock()
	a.logger.Debug().
		Str("session_id", string(sessionID)).
		Str("cwd", strings.TrimSpace(params.Cwd)).
		Int("mcp_server_count", len(mcpServers)).
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
	if err := a.ensureSessionBackend(ctx, sessionID); err != nil {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return acp.NewSessionResponse{}, err
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
	backend := state.backend
	a.mu.Unlock()
	if backend == nil {
		if err := a.ensureSessionBackend(ctx, params.SessionId); err != nil {
			return acp.PromptResponse{}, err
		}
	}

	a.mu.Lock()
	state, ok = a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidParams("session not found")
	}
	if state.cancel != nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("prompt already active for session")
	}
	if state.backend == nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, errors.New("session backend unavailable")
	}
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	threadID := state.thread
	cwd := state.cwd
	model := state.model
	mcpServers := state.mcpServers
	backend = state.backend
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if cur, exists := a.sessions[params.SessionId]; exists {
			cur.cancel = nil
		}
		a.mu.Unlock()
	}()

	toolID := acp.ToolCallId(fmt.Sprintf("codex-tool-%d", atomic.AddUint64(&a.nextToolID, 1)))
	toolName, toolArgs := buildCodexToolInvocation(threadID, cwd, userPrompt, a.defaultCodexConfig, model, mcpServers)
	callStartedAt := time.Now()
	a.logger.Info().
		Str("session_id", string(params.SessionId)).
		Str("tool_name", toolName).
		Str("tool_id", string(toolID)).
		Str("proto", "mcp").
		Str("method", "tools/call").
		Str("phase", "request").
		Msg("mcp tool call")
	if a.logger.Debug().Enabled() {
		a.logger.Debug().
			Str("session_id", string(params.SessionId)).
			Str("tool_name", toolName).
			Str("tool_id", string(toolID)).
			Str("tool_args", logJSON(toolArgs)).
			Str("proto", "mcp").
			Str("method", "tools/call").
			Str("phase", "request").
			Msg("invoking mcp tool")
	}
	if err := a.sendUpdate(promptCtx, params.SessionId, acp.StartToolCall(
		toolID,
		toolName,
		acp.WithStartKind(acp.ToolKindExecute),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
		acp.WithStartRawInput(toolArgs),
	)); err != nil {
		return acp.PromptResponse{}, err
	}

	result, err := backend.CallTool(promptCtx, &mcp.CallToolParams{
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
			a.logger.Info().
				Str("session_id", string(params.SessionId)).
				Str("tool_name", toolName).
				Str("tool_id", string(toolID)).
				Str("proto", "mcp").
				Str("method", "tools/call").
				Str("phase", "response").
				Str("status", "cancelled").
				Dur("duration", time.Since(callStartedAt)).
				Msg("mcp tool call")
			a.logger.Debug().Str("session_id", string(params.SessionId)).Msg("prompt canceled during tool call")
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		a.logger.Info().
			Str("session_id", string(params.SessionId)).
			Str("tool_name", toolName).
			Str("tool_id", string(toolID)).
			Str("proto", "mcp").
			Str("method", "tools/call").
			Str("phase", "response").
			Str("status", string(acp.ToolCallStatusFailed)).
			Dur("duration", time.Since(callStartedAt)).
			Msg("mcp tool call")
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
	if a.logger.Debug().Enabled() {
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
	}

	callStatus := acp.ToolCallStatusCompleted
	if result != nil && result.IsError {
		callStatus = acp.ToolCallStatusFailed
	}
	a.logger.Info().
		Str("session_id", string(params.SessionId)).
		Str("tool_name", toolName).
		Str("tool_id", string(toolID)).
		Str("proto", "mcp").
		Str("method", "tools/call").
		Str("phase", "response").
		Str("status", string(callStatus)).
		Dur("duration", time.Since(callStartedAt)).
		Msg("mcp tool call")

	thread, responseText := extractCodexToolResult(result)
	if thread != "" {
		a.mu.Lock()
		if cur, exists := a.sessions[params.SessionId]; exists {
			cur.thread = thread
		}
		a.mu.Unlock()
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
	nextMode := strings.TrimSpace(string(params.ModeId))
	backend, changed, err := a.setSessionConfig(params.SessionId, backendRestartReasonSessionSetMode, func(state *codexProxySessionState) bool {
		if state.mode == nextMode {
			return false
		}
		state.mode = nextMode
		return true
	})
	if err != nil {
		return acp.SetSessionModeResponse{}, err
	}
	if changed {
		a.closeBackendForRestart(params.SessionId, backend, backendRestartReasonSessionSetMode)
	}
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("mode", nextMode).
		Bool("reset_backend", changed).
		Msg("received set_session_mode")
	return acp.SetSessionModeResponse{}, nil
}

func (a *codexACPProxyAgent) SetSessionModel(_ context.Context, params acp.SetSessionModelRequest) (acp.SetSessionModelResponse, error) {
	nextModel := strings.TrimSpace(string(params.ModelId))
	backend, changed, err := a.setSessionConfig(params.SessionId, backendRestartReasonSessionSetModel, func(state *codexProxySessionState) bool {
		if state.model == nextModel {
			return false
		}
		state.model = nextModel
		return true
	})
	if err != nil {
		return acp.SetSessionModelResponse{}, err
	}
	if changed {
		a.closeBackendForRestart(params.SessionId, backend, backendRestartReasonSessionSetModel)
	}
	a.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("model", nextModel).
		Bool("reset_backend", changed).
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
	if a.logger.Trace().Enabled() {
		a.logger.Trace().
			Str("proto", "acp").
			Str("session_id", string(sessionID)).
			Str("update_type", sessionUpdateType(update)).
			Str("update_payload", sessionUpdatePayload(update)).
			Msg("sending session update")
	}
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
