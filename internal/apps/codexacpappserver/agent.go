package codexacp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

const (
	decisionAccept           = "accept"
	decisionAcceptForSession = "acceptForSession"
	decisionDecline          = "decline"
	decisionCancel           = "cancel"
	decisionApproved         = "approved"
	decisionApprovedSession  = "approved_for_session"
	decisionDenied           = "denied"
	decisionAbort            = "abort"
)

type codexACPConnection interface {
	SessionUpdate(ctx context.Context, params acp.SessionNotification) error
	RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type codexACPProxyAgent struct {
	agentName    string
	agentVersion string

	defaultConfig  codexAppConfig
	sessionFactory appServerBackendFactory
	logger         *zerolog.Logger

	connMu sync.RWMutex
	conn   codexACPConnection

	mu            sync.Mutex
	sessions      map[acp.SessionId]*codexProxySessionState
	nextSessionID uint64
}

type codexProxySessionState struct {
	cwd        string
	threadID   string
	turnID     string
	model      string
	mode       string
	mcpServers map[string]acp.McpServer

	backend appServerSession
	cancel  context.CancelFunc

	planDeltaByItem      map[string]string
	pendingRequests      map[string]string
	lastThreadStatusText string
	latestRateLimits     map[string]any
}

func newCodexACPProxyAgent(
	sessionFactory appServerBackendFactory,
	agentName string,
	defaultConfig codexAppConfig,
	logger *zerolog.Logger,
) *codexACPProxyAgent {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = DefaultAgentName
	}
	version := DefaultAgentVersion
	return &codexACPProxyAgent{
		agentName:      name,
		agentVersion:   version,
		defaultConfig:  defaultConfig.withModel(defaultConfig.Model),
		sessionFactory: sessionFactory,
		logger:         logger,
		sessions:       make(map[acp.SessionId]*codexProxySessionState),
	}
}

func (a *codexACPProxyAgent) setConnection(conn codexACPConnection) {
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
	return acp.AuthenticateResponse{}, nil
}

func (a *codexACPProxyAgent) Initialize(_ context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
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
	state, ok := a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	cancel := state.cancel
	backend := state.backend
	threadID := state.threadID
	turnID := state.turnID
	state.cancel = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if backend != nil {
		_ = backend.TurnInterrupt(context.Background(), threadID, turnID)
	}
	return nil
}

func (a *codexACPProxyAgent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	requestedSessionID, err := sessionIDFromNewSessionMeta(params.Meta)
	if err != nil {
		return acp.NewSessionResponse{}, acp.NewInvalidParams(err.Error())
	}
	sessionID := requestedSessionID
	if sessionID == "" {
		sessionID = acp.SessionId(fmt.Sprintf("session-%d", atomic.AddUint64(&a.nextSessionID, 1)))
	}
	var mcpServers map[string]acp.McpServer
	if len(params.McpServers) > 0 {
		mcpServers, err = validateMCPServers(params.McpServers)
		if err != nil {
			return acp.NewSessionResponse{}, acp.NewInvalidParams(err.Error())
		}
	}

	a.mu.Lock()
	if _, exists := a.sessions[sessionID]; exists {
		a.mu.Unlock()
		return acp.NewSessionResponse{}, acp.NewInvalidParams(fmt.Sprintf("session %q already exists", sessionID))
	}
	a.sessions[sessionID] = &codexProxySessionState{
		cwd:        strings.TrimSpace(params.Cwd),
		model:      a.defaultConfig.Model,
		mcpServers: mcpServers,
	}
	a.mu.Unlock()

	if err := a.ensureSessionBackend(ctx, sessionID); err != nil {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return acp.NewSessionResponse{}, err
	}
	if err := a.ensureSessionThread(ctx, sessionID); err != nil {
		a.mu.Lock()
		if state := a.sessions[sessionID]; state != nil && state.backend != nil {
			_ = state.backend.Close()
			_ = state.backend.Wait()
		}
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return acp.NewSessionResponse{}, err
	}

	resp := acp.NewSessionResponse{SessionId: sessionID}
	if a.defaultConfig.Model != "" {
		resp.Models = &acp.SessionModelState{
			CurrentModelId: acp.ModelId(a.defaultConfig.Model),
			AvailableModels: []acp.ModelInfo{
				{ModelId: acp.ModelId(a.defaultConfig.Model), Name: a.defaultConfig.Model},
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

	if err := a.ensureSessionBackend(ctx, params.SessionId); err != nil {
		return acp.PromptResponse{}, err
	}
	if err := a.ensureSessionThread(ctx, params.SessionId); err != nil {
		return acp.PromptResponse{}, err
	}

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
	if state.backend == nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, errors.New("session backend unavailable")
	}
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	backend := state.backend
	threadID := state.threadID
	model := state.model
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if current := a.sessions[params.SessionId]; current != nil {
			current.cancel = nil
			current.turnID = ""
			current.pendingRequests = nil
		}
		a.mu.Unlock()
	}()

	turnStart, err := backend.TurnStart(promptCtx, buildTurnStartParams(threadID, userPrompt, model))
	if err != nil {
		return acp.PromptResponse{}, fmt.Errorf("turn/start: %w", err)
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	a.mu.Lock()
	if current := a.sessions[params.SessionId]; current != nil {
		current.turnID = turnID
		current.planDeltaByItem = make(map[string]string)
		current.pendingRequests = make(map[string]string)
		current.lastThreadStatusText = ""
	}
	a.mu.Unlock()

	var usage map[string]any
	for {
		select {
		case <-promptCtx.Done():
			if errors.Is(promptCtx.Err(), context.Canceled) {
				return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
			return acp.PromptResponse{}, promptCtx.Err()
		case event, ok := <-backend.Events():
			if !ok {
				return acp.PromptResponse{}, errors.New("app-server event stream closed")
			}
			if event.Request != nil {
				if err := a.handleServerRequest(promptCtx, params.SessionId, event.Request); err != nil {
					return acp.PromptResponse{}, err
				}
				continue
			}
			if event.Notification == nil {
				continue
			}
			done, stopReason, usageUpdate, err := a.handleNotification(promptCtx, params.SessionId, threadID, turnID, event.Notification)
			if err != nil {
				return acp.PromptResponse{}, err
			}
			if usageUpdate != nil {
				usage = usageUpdate
			}
			if done {
				resp := acp.PromptResponse{StopReason: stopReason}
				meta := map[string]any{}
				if usage != nil {
					meta["usage"] = usage
				}
				if rateLimits := a.sessionRateLimits(params.SessionId); len(rateLimits) > 0 {
					meta["rateLimits"] = rateLimits
				}
				if len(meta) > 0 {
					resp.Meta = meta
				}
				return resp, nil
			}
		}
	}
}

func (a *codexACPProxyAgent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	nextMode := strings.TrimSpace(string(params.ModeId))
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams("session not found")
	}
	if state.cancel != nil {
		return acp.SetSessionModeResponse{}, acp.NewInvalidRequest("cannot update session mode while prompt is active")
	}
	state.mode = nextMode
	return acp.SetSessionModeResponse{}, nil
}

func (a *codexACPProxyAgent) SetSessionModel(_ context.Context, params acp.SetSessionModelRequest) (acp.SetSessionModelResponse, error) {
	nextModel := strings.TrimSpace(string(params.ModelId))
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		return acp.SetSessionModelResponse{}, acp.NewInvalidParams("session not found")
	}
	if state.cancel != nil {
		return acp.SetSessionModelResponse{}, acp.NewInvalidRequest("cannot update session model while prompt is active")
	}
	state.model = nextModel
	return acp.SetSessionModelResponse{}, nil
}

func (a *codexACPProxyAgent) ensureSessionBackend(ctx context.Context, sessionID acp.SessionId) error {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return acp.NewInvalidParams("session not found")
	}
	if state.backend != nil {
		a.mu.Unlock()
		return nil
	}
	sessionCWD := state.cwd
	a.mu.Unlock()

	backend, err := a.sessionFactory(ctx, sessionCWD)
	if err != nil {
		return fmt.Errorf("create app-server backend: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok = a.sessions[sessionID]
	if !ok {
		_ = backend.Close()
		_ = backend.Wait()
		return acp.NewInvalidParams("session not found")
	}
	if state.backend != nil {
		_ = backend.Close()
		_ = backend.Wait()
		return nil
	}
	state.backend = backend
	return nil
}

func (a *codexACPProxyAgent) ensureSessionThread(ctx context.Context, sessionID acp.SessionId) error {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return acp.NewInvalidParams("session not found")
	}
	if state.backend == nil {
		a.mu.Unlock()
		return errors.New("session backend unavailable")
	}
	if strings.TrimSpace(state.threadID) != "" {
		a.mu.Unlock()
		return nil
	}
	backend := state.backend
	cwd := state.cwd
	model := state.model
	mcpServers := state.mcpServers
	a.mu.Unlock()

	startResp, err := backend.ThreadStart(ctx, buildThreadStartParams(cwd, a.defaultConfig, model, mcpServers))
	if err != nil {
		return fmt.Errorf("thread/start: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok = a.sessions[sessionID]
	if !ok {
		return acp.NewInvalidParams("session not found")
	}
	state.threadID = strings.TrimSpace(startResp.Thread.ID)
	if strings.TrimSpace(state.model) == "" {
		state.model = strings.TrimSpace(startResp.Model)
	}
	return nil
}

func (a *codexACPProxyAgent) closeAllSessionBackends() {
	type backendEntry struct {
		backend appServerSession
		cancel  context.CancelFunc
	}
	entries := make([]backendEntry, 0)

	a.mu.Lock()
	for _, state := range a.sessions {
		if state.cancel != nil {
			entries = append(entries, backendEntry{backend: state.backend, cancel: state.cancel})
			state.cancel = nil
		} else {
			entries = append(entries, backendEntry{backend: state.backend, cancel: nil})
		}
		state.backend = nil
		state.threadID = ""
		state.turnID = ""
	}
	a.mu.Unlock()

	for _, entry := range entries {
		if entry.cancel != nil {
			entry.cancel()
		}
		if entry.backend != nil {
			_ = entry.backend.Close()
			_ = entry.backend.Wait()
		}
	}
}

func (a *codexACPProxyAgent) handleNotification(
	ctx context.Context,
	sessionID acp.SessionId,
	threadID string,
	turnID string,
	note *appServerNotification,
) (done bool, stopReason acp.StopReason, usage map[string]any, err error) {
	params, err := decodeJSONMap(note.Params)
	if err != nil {
		return false, "", nil, nil
	}
	if !matchesThreadID(params, threadID) {
		return false, "", nil, nil
	}
	if !matchesTurnID(params, turnID) {
		return false, "", nil, nil
	}

	switch note.Method {
	case "thread/started":
		thread := mapValue(params, "thread")
		startedThreadID := stringValue(thread, "id")
		if startedThreadID != "" {
			a.syncThreadID(sessionID, startedThreadID)
		}
	case "error":
		errObj := mapValue(params, "error")
		msg := stringValue(errObj, "message")
		details := rawStringValue(errObj, "additionalDetails")
		thought := strings.TrimSpace(msg)
		if thought == "" {
			thought = "Turn error."
		}
		if strings.TrimSpace(details) != "" {
			thought = fmt.Sprintf("%s %s", thought, strings.TrimSpace(details))
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
		willRetry, ok := boolValue(params, "willRetry")
		if ok && !willRetry {
			return true, acp.StopReasonRefusal, usageFromTokenNotification(params), nil
		}
	case "thread/status/changed":
		status := mapValue(params, "status")
		summary := threadStatusSummary(status)
		if summary == "" || !a.shouldEmitThreadStatus(sessionID, summary) {
			return false, "", nil, nil
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, "Thread status: "+summary); err != nil {
			return false, "", nil, err
		}
	case "turn/started":
		turn := mapValue(params, "turn")
		startedTurnID := stringValue(turn, "id")
		if startedTurnID == "" || startedTurnID == strings.TrimSpace(turnID) {
			a.resetTurnState(sessionID)
		}
	case "item/agentMessage/delta":
		delta := rawStringValue(params, "delta")
		if delta == "" {
			return false, "", nil, nil
		}
		if err := a.sendUpdate(ctx, sessionID, acp.UpdateAgentMessageText(delta)); err != nil {
			return false, "", nil, err
		}
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		delta := rawStringValue(params, "delta")
		if delta == "" {
			return false, "", nil, nil
		}
		if err := a.sendUpdate(ctx, sessionID, acp.UpdateAgentThoughtText(delta)); err != nil {
			return false, "", nil, err
		}
	case "item/reasoning/summaryPartAdded":
		summaryIndex, ok := int64Value(params, "summaryIndex")
		thought := "Reasoning summary updated."
		if ok {
			thought = fmt.Sprintf("Reasoning summary part added (#%d).", summaryIndex)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "item/plan/delta":
		itemID := stringValue(params, "itemId")
		delta := rawStringValue(params, "delta")
		if itemID == "" || delta == "" {
			return false, "", nil, nil
		}
		aggregated := a.appendPlanDelta(sessionID, itemID, delta)
		if strings.TrimSpace(aggregated) == "" {
			return false, "", nil, nil
		}
		if err := a.sendUpdate(ctx, sessionID, acp.UpdatePlan(acp.PlanEntry{
			Content:  aggregated,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   acp.PlanEntryStatusInProgress,
		})); err != nil {
			return false, "", nil, err
		}
	case "turn/plan/updated":
		entries := planEntriesFromNotification(params)
		if len(entries) > 0 {
			if err := a.sendUpdate(ctx, sessionID, acp.UpdatePlan(entries...)); err != nil {
				return false, "", nil, err
			}
		}
	case "turn/diff/updated":
		diff := rawStringValue(params, "diff")
		if diff == "" {
			return false, "", nil, nil
		}
		if err := a.sendUpdate(ctx, sessionID, acp.UpdateAgentThoughtText("Turn diff updated:\n"+diff)); err != nil {
			return false, "", nil, err
		}
	case "item/started":
		item := mapValue(params, "item")
		if len(item) == 0 {
			return false, "", nil, nil
		}
		itemType := stringValue(item, "type")
		itemID := stringValue(item, "id")
		if itemType == "" || itemID == "" {
			return false, "", nil, nil
		}
		title := toolCallTitle(itemType, item)
		update := acp.StartToolCall(
			toolCallID(itemID),
			title,
			acp.WithStartKind(toAppServerToolKind(itemType)),
			acp.WithStartStatus(toACPToolCallStatus(stringValue(item, "status"))),
			acp.WithStartRawInput(item),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "item/completed":
		item := mapValue(params, "item")
		if len(item) == 0 {
			return false, "", nil, nil
		}
		itemID := stringValue(item, "id")
		if itemID == "" {
			return false, "", nil, nil
		}
		status := toACPToolCallStatus(stringValue(item, "status"))
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(status),
			acp.WithUpdateRawOutput(item),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		itemID := stringValue(params, "itemId")
		delta := rawStringValue(params, "delta")
		if itemID == "" || delta == "" {
			return false, "", nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(delta))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "item/commandExecution/terminalInteraction":
		itemID := stringValue(params, "itemId")
		stdin := rawStringValue(params, "stdin")
		if itemID == "" || stdin == "" {
			return false, "", nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(stdin))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "item/autoApprovalReview/started":
		targetItemID := stringValue(params, "targetItemId")
		if targetItemID == "" {
			return false, "", nil, nil
		}
		review := mapValue(params, "review")
		title := "auto approval review"
		if status := stringValue(review, "status"); status != "" {
			title = fmt.Sprintf("auto approval review (%s)", status)
		}
		start := acp.StartToolCall(
			guardianToolCallID(targetItemID),
			title,
			acp.WithStartKind(acp.ToolKindOther),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(params),
		)
		if err := a.sendUpdate(ctx, sessionID, start); err != nil {
			return false, "", nil, err
		}
		if summary := guardianReviewSummary(review); summary != "" {
			update := acp.UpdateToolCall(
				guardianToolCallID(targetItemID),
				acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
			if err := a.sendUpdate(ctx, sessionID, update); err != nil {
				return false, "", nil, err
			}
		}
	case "item/autoApprovalReview/completed":
		targetItemID := stringValue(params, "targetItemId")
		if targetItemID == "" {
			return false, "", nil, nil
		}
		review := mapValue(params, "review")
		update := acp.UpdateToolCall(
			guardianToolCallID(targetItemID),
			acp.WithUpdateStatus(guardianReviewStatusToACPStatus(stringValue(review, "status"))),
			acp.WithUpdateRawOutput(params),
		)
		if summary := guardianReviewSummary(review); summary != "" {
			update = acp.UpdateToolCall(
				guardianToolCallID(targetItemID),
				acp.WithUpdateStatus(guardianReviewStatusToACPStatus(stringValue(review, "status"))),
				acp.WithUpdateRawOutput(params),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
		}
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "hook/started":
		run := mapValue(params, "run")
		runID := stringValue(run, "id")
		if runID == "" {
			return false, "", nil, nil
		}
		start := acp.StartToolCall(
			hookToolCallID(runID),
			hookRunTitle(run),
			acp.WithStartKind(acp.ToolKindExecute),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(run),
		)
		if err := a.sendUpdate(ctx, sessionID, start); err != nil {
			return false, "", nil, err
		}
	case "hook/completed":
		run := mapValue(params, "run")
		runID := stringValue(run, "id")
		if runID == "" {
			return false, "", nil, nil
		}
		update := acp.UpdateToolCall(
			hookToolCallID(runID),
			acp.WithUpdateStatus(hookRunStatusToACPStatus(stringValue(run, "status"))),
			acp.WithUpdateRawOutput(run),
		)
		if summary := hookRunSummary(run); summary != "" {
			update = acp.UpdateToolCall(
				hookToolCallID(runID),
				acp.WithUpdateStatus(hookRunStatusToACPStatus(stringValue(run, "status"))),
				acp.WithUpdateRawOutput(run),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
		}
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "item/mcpToolCall/progress":
		itemID := stringValue(params, "itemId")
		message := rawStringValue(params, "message")
		if itemID == "" || message == "" {
			return false, "", nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(message))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, err
		}
	case "serverRequest/resolved":
		requestID, ok := requestIDFromAny(params["requestId"])
		if ok {
			a.resolvePendingRequest(sessionID, requestID)
		}
	case "mcpServer/startupStatus/updated":
		name := stringValue(params, "name")
		status := stringValue(params, "status")
		errText := stringValue(params, "error")
		thought := fmt.Sprintf("MCP server %q status: %s.", name, status)
		if name == "" {
			thought = fmt.Sprintf("MCP server status: %s.", status)
		}
		if strings.TrimSpace(errText) != "" {
			thought += " " + errText
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "mcpServer/oauthLogin/completed":
		name := stringValue(params, "name")
		success, ok := boolValue(params, "success")
		errText := stringValue(params, "error")
		outcome := statusCompleted
		if ok && !success {
			outcome = statusFailed
		}
		thought := fmt.Sprintf("MCP OAuth login %s.", outcome)
		if name != "" {
			thought = fmt.Sprintf("MCP OAuth login for %q %s.", name, outcome)
		}
		if strings.TrimSpace(errText) != "" {
			thought += " " + errText
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "model/rerouted":
		fromModel := stringValue(params, "fromModel")
		toModel := stringValue(params, "toModel")
		reason := stringValue(params, "reason")
		thought := fmt.Sprintf("Model rerouted: %s -> %s.", fromModel, toModel)
		if fromModel == "" || toModel == "" {
			thought = "Model rerouted."
		}
		if reason != "" {
			thought = strings.TrimSpace(thought + " reason: " + reason + ".")
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "configWarning":
		summary := stringValue(params, "summary")
		path := stringValue(params, "path")
		details := stringValue(params, "details")
		thought := "Config warning."
		if summary != "" {
			thought = "Config warning: " + summary
		}
		if path != "" {
			thought += " path=" + path
		}
		if details != "" {
			thought += " " + details
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "deprecationNotice":
		summary := stringValue(params, "summary")
		details := stringValue(params, "details")
		thought := "Deprecation notice."
		if summary != "" {
			thought = "Deprecation notice: " + summary
		}
		if details != "" {
			thought += " " + details
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "account/login/completed":
		accountID := stringValue(params, "accountId")
		thought := "Account login completed."
		if accountID != "" {
			thought = fmt.Sprintf("Account login completed: %s.", accountID)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "account/updated":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Account updated."); err != nil {
			return false, "", nil, err
		}
	case "app/list/updated":
		if err := a.sendThoughtUpdate(ctx, sessionID, "App list updated."); err != nil {
			return false, "", nil, err
		}
	case "skills/changed":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Skills changed."); err != nil {
			return false, "", nil, err
		}
	case "thread/compacted":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Thread compacted."); err != nil {
			return false, "", nil, err
		}
	case "thread/archived":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Thread archived."); err != nil {
			return false, "", nil, err
		}
	case "thread/unarchived":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Thread unarchived."); err != nil {
			return false, "", nil, err
		}
	case "thread/closed":
		if err := a.sendThoughtUpdate(ctx, sessionID, "Thread closed."); err != nil {
			return false, "", nil, err
		}
	case "thread/name/updated":
		threadName := stringValue(params, "threadName")
		thought := "Thread name updated."
		if threadName != "" {
			thought = fmt.Sprintf("Thread name updated: %s.", threadName)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "windows/worldWritableWarning":
		extraCount, _ := int64Value(params, "extraCount")
		failedScan, _ := boolValue(params, "failedScan")
		samplePaths := listValue(params, "samplePaths")
		pathSamples := make([]string, 0, len(samplePaths))
		for _, rawPath := range samplePaths {
			path, _ := rawPath.(string)
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			pathSamples = append(pathSamples, path)
		}
		thought := fmt.Sprintf("Windows world-writable warning: %d additional path(s).", extraCount)
		if len(pathSamples) > 0 {
			thought = thought + " sample=" + strings.Join(pathSamples, ",")
		}
		if failedScan {
			thought += " scan_failed=true"
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "windowsSandbox/setupCompleted":
		mode := stringValue(params, "mode")
		success, _ := boolValue(params, "success")
		errText := stringValue(params, "error")
		outcome := statusFailed
		if success {
			outcome = "succeeded"
		}
		thought := fmt.Sprintf("Windows sandbox setup %s (mode=%s).", outcome, mode)
		if strings.TrimSpace(errText) != "" {
			thought += " " + errText
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/started":
		version := stringValue(params, "version")
		session := stringValue(params, "sessionId")
		thought := fmt.Sprintf("Realtime started (version=%s).", version)
		if session != "" {
			thought = fmt.Sprintf("Realtime started (version=%s, session=%s).", version, session)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/itemAdded":
		item := mapValue(params, "item")
		itemType := stringValue(item, "type")
		thought := "Realtime item added."
		if itemType != "" {
			thought = fmt.Sprintf("Realtime item added (type=%s).", itemType)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/outputAudio/delta":
		audio := mapValue(params, "audio")
		itemID := stringValue(audio, "itemId")
		sampleRate, _ := int64Value(audio, "sampleRate")
		numChannels, _ := int64Value(audio, "numChannels")
		data := rawStringValue(audio, "data")
		thought := fmt.Sprintf("Realtime audio delta: sampleRate=%d channels=%d bytes=%d.", sampleRate, numChannels, len(data))
		if itemID != "" {
			thought = fmt.Sprintf("Realtime audio delta: item=%s sampleRate=%d channels=%d bytes=%d.", itemID, sampleRate, numChannels, len(data))
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/transcriptUpdated":
		role := stringValue(params, "role")
		text := rawStringValue(params, "text")
		thought := "Realtime transcript updated."
		if role != "" || text != "" {
			thought = fmt.Sprintf("Realtime transcript (%s): %s", role, text)
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/error":
		message := stringValue(params, "message")
		thought := "Realtime error."
		if message != "" {
			thought = "Realtime error: " + message
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "thread/realtime/closed":
		reason := stringValue(params, "reason")
		thought := "Realtime closed."
		if reason != "" {
			thought = "Realtime closed: " + reason
		}
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "fs/changed":
		watchID := stringValue(params, "watchId")
		changedPaths := listValue(params, "changedPaths")
		pathCount := len(changedPaths)
		thought := fmt.Sprintf("Filesystem changed: watch=%s paths=%d.", watchID, pathCount)
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "fuzzyFileSearch/sessionUpdated":
		session := stringValue(params, "sessionId")
		query := stringValue(params, "query")
		files := listValue(params, "files")
		thought := fmt.Sprintf("Fuzzy search update: session=%s query=%q files=%d.", session, query, len(files))
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "fuzzyFileSearch/sessionCompleted":
		session := stringValue(params, "sessionId")
		thought := fmt.Sprintf("Fuzzy search completed: session=%s.", session)
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "command/exec/outputDelta":
		processID := stringValue(params, "processId")
		stream := stringValue(params, "stream")
		delta := rawStringValue(params, "deltaBase64")
		capReached, _ := boolValue(params, "capReached")
		thought := fmt.Sprintf("command/exec output: process=%s stream=%s bytes(base64)=%d capReached=%t.", processID, stream, len(delta), capReached)
		if err := a.sendThoughtUpdate(ctx, sessionID, thought); err != nil {
			return false, "", nil, err
		}
	case "account/rateLimits/updated":
		rateLimits := mapValue(params, "rateLimits")
		if len(rateLimits) > 0 {
			a.setSessionRateLimits(sessionID, rateLimits)
		}
	case "thread/tokenUsage/updated":
		usage = usageFromTokenNotification(params)
	case "turn/completed":
		turn := mapValue(params, "turn")
		status := stringValue(turn, "status")
		return true, stopReasonFromTurnStatus(status), usageFromTokenNotification(params), nil
	}
	return false, "", usage, nil
}

func (a *codexACPProxyAgent) handleServerRequest(ctx context.Context, sessionID acp.SessionId, req *appServerRequest) error {
	if req == nil {
		return nil
	}

	a.mu.Lock()
	state := a.sessions[sessionID]
	a.mu.Unlock()
	if state == nil || state.backend == nil {
		return acp.NewInvalidParams("session backend unavailable")
	}
	requestID := canonicalRequestID(req.ID)
	if requestID != "" {
		a.markPendingRequest(sessionID, requestID, req.Method)
	}

	switch req.Method {
	case "item/commandExecution/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Command approval", acp.ToolKindExecute, params, listValue(params, "availableDecisions"), []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{"decision": decision})
	case "item/fileChange/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "File change approval", acp.ToolKindEdit, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{"decision": decision})
	case "item/permissions/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		requestedPermissions := mapValue(params, "permissions")
		decision, err := a.requestDecision(ctx, sessionID, "Permissions approval", acp.ToolKindOther, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		switch decisionName {
		case decisionAcceptForSession:
			return state.backend.RespondRequest(ctx, req, map[string]any{
				"permissions": requestedPermissions,
				"scope":       "session",
			})
		case decisionAccept:
			return state.backend.RespondRequest(ctx, req, map[string]any{
				"permissions": requestedPermissions,
				"scope":       "turn",
			})
		default:
			return state.backend.RespondRequest(ctx, req, map[string]any{
				"permissions": map[string]any{},
				"scope":       "turn",
			})
		}
	case "item/tool/call":
		params, _ := decodeJSONMap(req.Params)
		toolName := stringValue(params, "tool")
		title := "Tool call request"
		if toolName != "" {
			title = fmt.Sprintf("Tool call request: %s", toolName)
		}
		decision, err := a.requestDecision(ctx, sessionID, title, acp.ToolKindExecute, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		statusText := "declined by user"
		switch decisionName {
		case decisionAccept, decisionAcceptForSession:
			statusText = "approved by user, not executed by ACP bridge"
		case decisionCancel:
			statusText = "cancelled by user"
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{
			"success": false,
			"contentItems": []any{
				map[string]any{
					"type": "inputText",
					"text": "Dynamic tool call " + statusText + ".",
				},
			},
		})
	case "item/tool/requestUserInput":
		params, _ := decodeJSONMap(req.Params)
		answers := map[string]any{}
		for _, rawQuestion := range listValue(params, "questions") {
			question, ok := rawQuestion.(map[string]any)
			if !ok {
				continue
			}
			questionID := stringValue(question, "id")
			if questionID == "" {
				continue
			}
			title := "User input request"
			if header := stringValue(question, "header"); header != "" {
				title = "User input: " + header
			}
			decisions := questionDecisionOptions(question)
			selected, err := a.requestDecision(ctx, sessionID, title, acp.ToolKindOther, mergeRequestInput(params, map[string]any{"question": question}), decisions, nil)
			if err != nil {
				return err
			}
			answerValues := []string{}
			selectedText, _ := selected.(string)
			if selectedText != "" && selectedText != decisionCancel && selectedText != decisionDecline {
				answerValues = append(answerValues, selectedText)
			}
			answers[questionID] = map[string]any{"answers": answerValues}
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{"answers": answers})
	case "mcpServer/elicitation/request":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "MCP elicitation request", acp.ToolKindOther, params, nil, []any{decisionAccept, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		action := decisionCancel
		switch decisionName {
		case decisionAccept, decisionAcceptForSession:
			action = decisionAccept
		case decisionDecline:
			action = decisionDecline
		case decisionCancel:
			action = decisionCancel
		}
		resp := map[string]any{
			"action": action,
		}
		if action == decisionAccept {
			resp["content"] = map[string]any{}
		}
		if meta, ok := params["_meta"]; ok {
			resp["_meta"] = meta
		}
		return state.backend.RespondRequest(ctx, req, resp)
	case "applyPatchApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Patch approval", acp.ToolKindEdit, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{
			"decision": legacyApprovalDecision(decision),
		})
	case "execCommandApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Exec command approval", acp.ToolKindExecute, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return state.backend.RespondRequest(ctx, req, map[string]any{
			"decision": legacyApprovalDecision(decision),
		})
	case "account/chatgptAuthTokens/refresh":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "ChatGPT token refresh", acp.ToolKindOther, params, nil, []any{decisionAccept, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		if decisionName == decisionCancel || decisionName == decisionDecline {
			return state.backend.RespondRequestError(ctx, req, -32000, "chatgpt token refresh declined", nil)
		}
		resp, ok := chatgptAuthTokensFromEnv()
		if !ok {
			return state.backend.RespondRequestError(
				ctx,
				req,
				-32001,
				"chatgpt token refresh unavailable: set CODEX_CHATGPT_ACCESS_TOKEN and CODEX_CHATGPT_ACCOUNT_ID",
				nil,
			)
		}
		return state.backend.RespondRequest(ctx, req, resp)
	default:
		return a.respondWithFallback(ctx, state.backend, req)
	}
}

func (a *codexACPProxyAgent) respondWithFallback(ctx context.Context, backend appServerSession, req *appServerRequest) error {
	if backend == nil || req == nil {
		return nil
	}
	switch req.Method {
	case "item/commandExecution/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decisionCancel})
	case "item/fileChange/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decisionDecline})
	case "item/permissions/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		})
	default:
		return backend.RespondRequestError(ctx, req, -32601, "unsupported server request", map[string]any{"method": req.Method})
	}
}

func (a *codexACPProxyAgent) requestDecision(
	ctx context.Context,
	sessionID acp.SessionId,
	title string,
	toolKind acp.ToolKind,
	rawInput map[string]any,
	availableDecisions []any,
	defaultDecisions []any,
) (any, error) {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return nil, errors.New("acp connection is not initialized")
	}

	decisions := availableDecisions
	if len(decisions) == 0 {
		decisions = defaultDecisions
	}
	options, byOptionID := permissionOptions(decisions)
	if len(options) == 0 {
		options, byOptionID = permissionOptions(defaultDecisions)
	}

	req := acp.RequestPermissionRequest{
		SessionId: sessionID,
		ToolCall: acp.RequestPermissionToolCall{
			ToolCallId: toolCallID(stringValue(rawInput, "itemId")),
			Title:      acp.Ptr(title),
			Kind:       acp.Ptr(toolKind),
			RawInput:   rawInput,
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: options,
	}
	resp, err := conn.RequestPermission(ctx, req)
	if err != nil {
		return decisionCancel, nil
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId == "" {
		return decisionCancel, nil
	}
	decision := byOptionID[resp.Outcome.Selected.OptionId]
	if decision == nil {
		return decisionCancel, nil
	}
	return decision, nil
}

func permissionOptions(decisions []any) ([]acp.PermissionOption, map[acp.PermissionOptionId]any) {
	options := make([]acp.PermissionOption, 0, len(decisions))
	byOptionID := make(map[acp.PermissionOptionId]any, len(decisions))
	for i, decision := range decisions {
		optionID := acp.PermissionOptionId(fmt.Sprintf("opt-%d", i+1))
		option := acp.PermissionOption{
			OptionId: optionID,
			Name:     permissionOptionLabel(decision),
			Kind:     permissionOptionKind(decision),
		}
		options = append(options, option)
		byOptionID[optionID] = decision
	}
	return options, byOptionID
}

func permissionOptionKind(decision any) acp.PermissionOptionKind {
	switch v := decision.(type) {
	case string:
		switch strings.TrimSpace(v) {
		case decisionAccept:
			return acp.PermissionOptionKindAllowOnce
		case decisionAcceptForSession:
			return acp.PermissionOptionKindAllowAlways
		case decisionDecline, decisionCancel:
			return acp.PermissionOptionKindRejectOnce
		case decisionApproved:
			return acp.PermissionOptionKindAllowOnce
		case decisionApprovedSession:
			return acp.PermissionOptionKindAllowAlways
		case decisionDenied, decisionAbort:
			return acp.PermissionOptionKindRejectOnce
		default:
			return acp.PermissionOptionKindAllowOnce
		}
	default:
		return acp.PermissionOptionKindAllowAlways
	}
}

func permissionOptionLabel(decision any) string {
	switch v := decision.(type) {
	case string:
		switch strings.TrimSpace(v) {
		case decisionAccept:
			return "Allow once"
		case decisionAcceptForSession:
			return "Allow for session"
		case decisionDecline:
			return "Decline"
		case decisionCancel:
			return "Cancel"
		case decisionApproved:
			return "Approve once"
		case decisionApprovedSession:
			return "Approve for session"
		case decisionDenied:
			return "Deny"
		case decisionAbort:
			return "Abort"
		default:
			return strings.TrimSpace(v)
		}
	default:
		return "Allow"
	}
}

func questionDecisionOptions(question map[string]any) []any {
	questionOptions := listValue(question, "options")
	if len(questionOptions) == 0 {
		return []any{decisionAccept, decisionDecline, decisionCancel}
	}
	decisions := make([]any, 0, len(questionOptions)+1)
	for _, raw := range questionOptions {
		opt, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		label := stringValue(opt, "label")
		if label == "" {
			continue
		}
		decisions = append(decisions, label)
	}
	decisions = append(decisions, decisionCancel)
	return decisions
}

func mergeRequestInput(base map[string]any, extra map[string]any) map[string]any {
	out := cloneAnyMap(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func legacyApprovalDecision(decision any) string {
	decisionName, _ := decision.(string)
	switch strings.TrimSpace(decisionName) {
	case decisionAcceptForSession:
		return decisionApprovedSession
	case decisionDecline:
		return decisionDenied
	case decisionCancel:
		return decisionAbort
	default:
		return decisionApproved
	}
}

func chatgptAuthTokensFromEnv() (map[string]any, bool) {
	accessToken := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_ACCESS_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_ACCOUNT_ID"))
	if accessToken == "" || accountID == "" {
		return nil, false
	}
	resp := map[string]any{
		"accessToken":      accessToken,
		"chatgptAccountId": accountID,
	}
	if planType := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_PLAN_TYPE")); planType != "" {
		resp["chatgptPlanType"] = planType
	}
	return resp, true
}

func sessionIDFromNewSessionMeta(meta any) (acp.SessionId, error) {
	if meta == nil {
		return "", nil
	}
	metaMap, ok := meta.(map[string]any)
	if !ok {
		return "", fmt.Errorf("session/new _meta must be an object")
	}
	rawSessionID, ok := metaMap["sessionId"]
	if !ok {
		return "", nil
	}
	sessionID, ok := rawSessionID.(string)
	if !ok {
		return "", fmt.Errorf("session/new _meta.sessionId must be a string")
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return "", fmt.Errorf("session/new _meta.sessionId must not be empty")
	}
	return acp.SessionId(trimmed), nil
}

func (a *codexACPProxyAgent) sendUpdate(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) error {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return errors.New("acp connection is not initialized")
	}
	return conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    update,
	})
}

func decodeJSONMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func mapValue(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	typed, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func listValue(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	typed, ok := v.([]any)
	if !ok {
		return nil
	}
	return typed
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func rawStringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func boolValue(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	typed, ok := v.(bool)
	if !ok {
		return false, false
	}
	return typed, true
}

func int64Value(m map[string]any, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch typed := v.(type) {
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	default:
		return 0, false
	}
}

func matchesThreadID(params map[string]any, threadID string) bool {
	if strings.TrimSpace(threadID) == "" {
		return true
	}
	if got := stringValue(params, "threadId"); got != "" && got != strings.TrimSpace(threadID) {
		return false
	}
	return true
}

func matchesTurnID(params map[string]any, turnID string) bool {
	if strings.TrimSpace(turnID) == "" {
		return true
	}
	if got := stringValue(params, "turnId"); got != "" && got != strings.TrimSpace(turnID) {
		return false
	}
	return true
}

func planEntriesFromNotification(params map[string]any) []acp.PlanEntry {
	planSteps := listValue(params, "plan")
	if len(planSteps) == 0 {
		return nil
	}
	entries := make([]acp.PlanEntry, 0, len(planSteps))
	for _, rawStep := range planSteps {
		stepMap, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		stepText := stringValue(stepMap, "step")
		if stepText == "" {
			continue
		}
		entries = append(entries, acp.PlanEntry{
			Content:  stepText,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   toACPPlanStatus(stringValue(stepMap, "status")),
		})
	}
	return entries
}

func usageFromTokenNotification(params map[string]any) map[string]any {
	tokenUsage := mapValue(params, "tokenUsage")
	if len(tokenUsage) == 0 {
		return nil
	}
	last := mapValue(tokenUsage, "last")
	if len(last) == 0 {
		return nil
	}
	usage := map[string]any{}
	if v, ok := last["inputTokens"]; ok {
		usage["inputTokens"] = v
	}
	if v, ok := last["outputTokens"]; ok {
		usage["outputTokens"] = v
	}
	if v, ok := last["totalTokens"]; ok {
		usage["totalTokens"] = v
	}
	if v, ok := last["cachedInputTokens"]; ok {
		usage["cachedReadTokens"] = v
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func stopReasonFromTurnStatus(status string) acp.StopReason {
	switch strings.TrimSpace(status) {
	case "interrupted":
		return acp.StopReasonCancelled
	case "failed":
		return acp.StopReasonRefusal
	default:
		return acp.StopReasonEndTurn
	}
}

func toolCallID(itemID string) acp.ToolCallId {
	trimmed := strings.TrimSpace(itemID)
	if trimmed == "" {
		return acp.ToolCallId("codex-item-unknown")
	}
	return acp.ToolCallId("codex-item-" + trimmed)
}

func guardianToolCallID(targetItemID string) acp.ToolCallId {
	return syntheticToolCallID("guardian", targetItemID)
}

func hookToolCallID(runID string) acp.ToolCallId {
	return syntheticToolCallID("hook", runID)
}

func syntheticToolCallID(prefix string, value string) acp.ToolCallId {
	trimmedPrefix := strings.TrimSpace(prefix)
	trimmedValue := strings.TrimSpace(value)
	if trimmedPrefix == "" {
		trimmedPrefix = "synthetic"
	}
	if trimmedValue == "" {
		trimmedValue = "unknown"
	}
	return acp.ToolCallId("codex-" + trimmedPrefix + "-" + trimmedValue)
}

func guardianReviewStatusToACPStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case "approved":
		return acp.ToolCallStatusCompleted
	case "denied", "aborted":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func guardianReviewSummary(review map[string]any) string {
	if len(review) == 0 {
		return ""
	}
	status := stringValue(review, "status")
	riskLevel := stringValue(review, "riskLevel")
	rationale := stringValue(review, "rationale")
	parts := make([]string, 0, 3)
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if riskLevel != "" {
		parts = append(parts, "risk="+riskLevel)
	}
	if rationale != "" {
		parts = append(parts, rationale)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func hookRunStatusToACPStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case "completed":
		return acp.ToolCallStatusCompleted
	case "failed", "blocked", "stopped":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func hookRunTitle(run map[string]any) string {
	eventName := stringValue(run, "eventName")
	if eventName == "" {
		return "hook"
	}
	return "hook " + eventName
}

func hookRunSummary(run map[string]any) string {
	if len(run) == 0 {
		return ""
	}
	status := stringValue(run, "status")
	statusMessage := stringValue(run, "statusMessage")
	parts := make([]string, 0, 2)
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if statusMessage != "" {
		parts = append(parts, statusMessage)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func threadStatusSummary(status map[string]any) string {
	statusType := stringValue(status, "type")
	if statusType == "" {
		return ""
	}
	flags := listValue(status, "activeFlags")
	if len(flags) == 0 {
		return statusType
	}
	flagValues := make([]string, 0, len(flags))
	for _, raw := range flags {
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		flagValues = append(flagValues, strings.TrimSpace(s))
	}
	if len(flagValues) == 0 {
		return statusType
	}
	sort.Strings(flagValues)
	return fmt.Sprintf("%s (%s)", statusType, strings.Join(flagValues, ","))
}

func (a *codexACPProxyAgent) sendThoughtUpdate(ctx context.Context, sessionID acp.SessionId, text string) error {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	return a.sendUpdate(ctx, sessionID, acp.UpdateAgentThoughtText(trimmed))
}

func (a *codexACPProxyAgent) resetTurnState(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.planDeltaByItem = make(map[string]string)
		state.pendingRequests = make(map[string]string)
		state.lastThreadStatusText = ""
	}
}

func (a *codexACPProxyAgent) appendPlanDelta(sessionID acp.SessionId, itemID string, delta string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return delta
	}
	if state.planDeltaByItem == nil {
		state.planDeltaByItem = make(map[string]string)
	}
	state.planDeltaByItem[itemID] += delta
	return state.planDeltaByItem[itemID]
}

func (a *codexACPProxyAgent) shouldEmitThreadStatus(sessionID acp.SessionId, statusText string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return true
	}
	if state.lastThreadStatusText == statusText {
		return false
	}
	state.lastThreadStatusText = statusText
	return true
}

func (a *codexACPProxyAgent) setSessionRateLimits(sessionID acp.SessionId, rateLimits map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.latestRateLimits = cloneAnyMap(rateLimits)
	}
}

func (a *codexACPProxyAgent) sessionRateLimits(sessionID acp.SessionId) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		return cloneAnyMap(state.latestRateLimits)
	}
	return nil
}

func cloneAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (a *codexACPProxyAgent) syncThreadID(sessionID acp.SessionId, nextThreadID string) {
	trimmed := strings.TrimSpace(nextThreadID)
	if trimmed == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.threadID = trimmed
	}
}

func (a *codexACPProxyAgent) markPendingRequest(sessionID acp.SessionId, requestID string, method string) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if trimmedRequestID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	if state.pendingRequests == nil {
		state.pendingRequests = make(map[string]string)
	}
	state.pendingRequests[trimmedRequestID] = strings.TrimSpace(method)
}

func (a *codexACPProxyAgent) resolvePendingRequest(sessionID acp.SessionId, requestID string) bool {
	trimmedRequestID := strings.TrimSpace(requestID)
	if trimmedRequestID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || state.pendingRequests == nil {
		return false
	}
	if _, ok := state.pendingRequests[trimmedRequestID]; !ok {
		return false
	}
	delete(state.pendingRequests, trimmedRequestID)
	return true
}

func requestIDFromAny(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return canonicalRequestID(raw), true
}

func toolCallTitle(itemType string, item map[string]any) string {
	switch strings.TrimSpace(itemType) {
	case "commandExecution":
		if command := stringValue(item, "command"); command != "" {
			return command
		}
		return "command execution"
	case "fileChange":
		return "file change"
	case "mcpToolCall":
		tool := stringValue(item, "tool")
		if tool != "" {
			return tool
		}
		return "mcp tool call"
	case "dynamicToolCall":
		tool := stringValue(item, "tool")
		if tool != "" {
			return tool
		}
		return "dynamic tool call"
	default:
		return itemType
	}
}
