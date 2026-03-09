package acpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
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
	// ClientName is the name reported to the ACP server during initialization.
	ClientName string
	// ClientVersion is the version reported to the ACP server during initialization.
	ClientVersion string
	// Command is the argv array used to start the ACP subprocess.
	Command []string
	// WorkingDir is the directory where the ACP subprocess is executed.
	WorkingDir string
	// Stderr is an optional writer for the ACP subprocess's standard error.
	Stderr io.Writer
	// PermissionHandler decides how to respond to ACP permission requests.
	PermissionHandler PermissionHandler
	// Logger is the zerolog logger to use for this agent.
	Logger *zerolog.Logger
}

// Agent adapts an Agentic Computing Protocol (ACP) runtime to the ADK agent interface.
// It manages the lifecycle of an ACP subprocess and maps ACP sessions to ADK sessions.
type Agent struct {
	adkagent.Agent

	client       *Client
	workingDir   string
	sessionModel string
	logger       zerolog.Logger
	sessionMu    sync.Mutex
	remoteByADK  map[string]string
}

const (
	defaultAgentName        = "ACPAgent"
	defaultAgentDescription = "ACP runtime exposed through ADK"

	acpTypeText     = "text"
	acpTypeImage    = "image"
	acpTypeAudio    = "audio"
	acpTypeResource = "resource"
)

var _ adkagent.Agent = (*Agent)(nil)

// New creates an ADK agent backed by an ACP client process. It starts the process
// and performs protocol initialization. The caller is responsible for calling
// Close() to shut down the subprocess.
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
	})
	if err != nil {
		return nil, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize acp client: %w", err)
	}

	a := &Agent{
		client:       client,
		workingDir:   cfg.WorkingDir,
		sessionModel: strings.TrimSpace(cfg.Model),
		logger:       l,
		remoteByADK:  make(map[string]string),
	}
	base, err := adkagent.New(adkagent.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
		Run:         a.run,
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("create adk acp agent: %w", err)
	}
	a.Agent = base
	return a, nil
}

// Close shuts down the underlying ACP client process.
func (a *Agent) Close() error {
	return a.client.Close()
}

func (a *Agent) run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		remoteSessionID, err := a.ensureRemoteSession(ctx, ctx.Session().ID())
		if err != nil {
			yield(nil, err)
			return
		}

		prompt := extractPromptText(ctx.UserContent())
		if strings.TrimSpace(prompt) == "" {
			yield(nil, fmt.Errorf("prompt is empty"))
			return
		}

		a.logger.Debug().
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
			case note, ok := <-updates:
				if !ok {
					updates = nil
					continue
				}
				ev, ok := mapACPUpdateToEvent(a.logger, ctx.InvocationID(), note.Update)
				if !ok {
					continue
				}
				// We log but don't re-mark as partial here as mapACPUpdateToEvent 
				// already set the appropriate Partial flag.
				a.logADKEvent(ev, "yielding adk event")
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

		a.logger.Debug().
			Str("adk_session_id", ctx.Session().ID()).
			Str("acp_session_id", remoteSessionID).
			Msg("completed adk invocation")

		ev := session.NewEvent(ctx.InvocationID())
		if promptResult != nil {
			ev.FinishReason = mapACPStopReasonToFinishReason(promptResult.Response.StopReason)
			ev.UsageMetadata = mapACPUsageToUsageMetadata(promptResult.Usage)
		}
		ev.TurnComplete = true
		a.logADKEvent(ev, "yielding final turn complete event")
		if !yield(ev, nil) {
			return
		}
	}
}


func (a *Agent) logADKEvent(ev *session.Event, msg string) {
	if ev == nil {
		return
	}
	logEvent := a.logger.Debug().
		Str("invocation_id", ev.InvocationID).
		Bool("partial", ev.Partial).
		Bool("turn_complete", ev.TurnComplete)

	if ev.FinishReason != "" {
		logEvent = logEvent.Str("finish_reason", string(ev.FinishReason))
	}
	if ev.UsageMetadata != nil {
		logEvent = logEvent.Int32("prompt_tokens", ev.UsageMetadata.PromptTokenCount).
			Int32("candidates_tokens", ev.UsageMetadata.CandidatesTokenCount).
			Int32("total_tokens", ev.UsageMetadata.TotalTokenCount)
	}
	if ev.Content != nil {
		logEvent = logEvent.Int("parts_count", len(ev.Content.Parts))
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
	if val, ok := usage["inputTokens"].(float64); ok {
		m.PromptTokenCount = int32(val)
	}
	if val, ok := usage["outputTokens"].(float64); ok {
		m.CandidatesTokenCount = int32(val)
	}
	if val, ok := usage["totalTokens"].(float64); ok {
		m.TotalTokenCount = int32(val)
	}
	if val, ok := usage["cachedReadTokens"].(float64); ok {
		m.CachedContentTokenCount = int32(val)
	}
	return m
}


func (a *Agent) ensureRemoteSession(ctx context.Context, adkSessionID string) (string, error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if sessionID := a.remoteByADK[adkSessionID]; sessionID != "" {
		a.logger.Debug().Str("adk_session_id", adkSessionID).Str("acp_session_id", sessionID).Msg("reusing acp session for adk session")
		return sessionID, nil
	}
	resp, err := a.client.CreateSession(ctx, a.workingDir, a.sessionModel)
	if err != nil {
		return "", err
	}
	sessionID := string(resp.SessionId)
	a.remoteByADK[adkSessionID] = sessionID
	event := a.logger.Debug().
		Str("adk_session_id", adkSessionID).
		Str("acp_session_id", sessionID)
	if a.sessionModel != "" {
		event = event.Str("model", a.sessionModel)
	}
	event.Msg("created new acp session for adk session")
	return sessionID, nil
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

func mapACPUpdateToEvent(logger zerolog.Logger, invocationID string, update acp.SessionUpdate) (*session.Event, bool) {
	switch {
	case update.AgentMessageChunk != nil:
		return mapACPAgentMessageChunk(logger, invocationID, update.AgentMessageChunk)
	case update.UserMessageChunk != nil:
		return mapACPUserMessageChunk(logger, invocationID, update.UserMessageChunk)
	case update.AgentThoughtChunk != nil:
		return mapACPAgentThoughtChunk(logger, invocationID, update.AgentThoughtChunk)
	case update.ToolCall != nil:
		return mapACPToolCall(invocationID, update.ToolCall)
	case update.ToolCallUpdate != nil:
		return mapACPToolCallUpdate(invocationID, update.ToolCallUpdate)
	case update.Plan != nil:
		logIgnoredACPUpdate(logger, "plan", map[string]any{"entries": update.Plan.Entries})
		return nil, false
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
		logUnsupportedACPUpdate(logger, update)
		return nil, false
	}
}

func mapACPAgentMessageChunk(logger zerolog.Logger, invocationID string, chunk *acp.SessionUpdateAgentMessageChunk) (*session.Event, bool) {
	part, ok := mapACPContentBlockToPart(logger, chunk.Content)
	if !ok {
		return nil, false
	}
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	ev.Partial = true
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


func mapACPContentBlockToPart(logger zerolog.Logger, block acp.ContentBlock) (*genai.Part, bool) {
	if block.Text != nil {
		if block.Text.Text == "" {
			return nil, false
		}
		return genai.NewPartFromText(block.Text.Text), true
	}
	logIgnoredACPContentBlock(logger, block)
	return nil, false
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

func logUnsupportedACPUpdate(logger zerolog.Logger, update acp.SessionUpdate) {
	logEvent := logger.Debug().
		Str("acp_update_type", sessionUpdateType(update))

	if payload, ok := marshalACPUpdatePayload(logger, "session_update_"+sessionUpdateType(update), update); ok {
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
		// Check if it's an empty update which might mean an unrecognized discriminator
		// such as usage_update in older SDK versions.
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
