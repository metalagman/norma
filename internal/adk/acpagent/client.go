package acpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

var (
	// ErrPromptAlreadyActive is returned when a prompt is already in progress for
	// the same ACP session ID.
	ErrPromptAlreadyActive = errors.New("acp prompt already active")

	errSessionIDRequired = errors.New("acp session id is required")
	errPromptRequired    = errors.New("acp prompt is required")
	errModelRequired     = errors.New("acp model is required")
)

const (
	defaultClientName    = "norma-acpagent"
	defaultClientVersion = "dev"
	unknownValue         = "unknown"

	// idleUpdateWindow is the duration to wait for further updates before considering
	// a series of ACP updates complete.
	idleUpdateWindow = 20 * time.Millisecond
)

// PermissionHandler decides how ACP permission requests should be handled.
// It returns a response with the selected outcome or an error if the request
// could not be processed.
type PermissionHandler func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)

// ClientConfig configures an ACP subprocess client.
type ClientConfig struct {
	// Command is the argv array used to start the ACP subprocess.
	Command []string
	// WorkingDir is the directory where the ACP subprocess is executed.
	WorkingDir string
	// ClientName is the name reported to the ACP server. Defaults to "norma-acpagent".
	ClientName string
	// ClientVersion is the version reported to the ACP server. Defaults to "dev".
	ClientVersion string
	// Stderr is an optional writer for the ACP subprocess's standard error.
	Stderr io.Writer
	// PermissionHandler decides how to respond to ACP permission requests.
	PermissionHandler PermissionHandler
	// OnSessionNewSetModel decides whether to call session/set_model after session/new.
	// Defaults to false.
	OnSessionNewSetModel bool
	// Logger is the zerolog logger to use for this client.
	Logger *zerolog.Logger
}

// ExtendedSessionNotification wraps an ACP notification with its raw JSON representation
// to allow access to fields not yet supported by the SDK.
type ExtendedSessionNotification struct {
	acp.SessionNotification
	Raw json.RawMessage
}

// Client manages a single Agentic Computing Protocol (ACP) subprocess and its
// communication over standard input/output. It implements the acp.Client interface
// to handle protocol-level callbacks and manages multiple concurrent prompt sessions.
type Client struct {
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	conn                  *acp.ClientSideConnection
	permissionHandler     PermissionHandler
	onSessionNewSetModel  bool
	clientName            string
	clientVersion         string
	logger            zerolog.Logger

	stateMu         sync.Mutex
	activeBySession map[acp.SessionId]*activePrompt
	updates         chan ExtendedSessionNotification
	deactivate      chan acp.SessionId

	closed       chan struct{}
	dispatchDone chan struct{}
	closeOnce    sync.Once
	closeErr     error
}

type activePrompt struct {
	sessionID acp.SessionId
	updates   chan ExtendedSessionNotification
	signal    chan struct{}
	lastChunk *loggedACPChunk
	closeOnce sync.Once
}


type loggedACPChunk struct {
	kind         string
	contentBlock map[string]any
	partial      bool
	thought      bool
}

// PromptResult contains the terminal Prompt RPC response, usage metadata, or an error.
type PromptResult struct {
	Response acp.PromptResponse
	Usage    map[string]any
	Err      error
}


var _ acp.Client = (*Client)(nil)

// NewClient starts an ACP subprocess and returns a protocol client over stdio.
func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("acp command is required")
	}

	l := zerolog.Nop()
	if cfg.Logger != nil {
		l = cfg.Logger.With().Str("subcomponent", "acpagent.client").Logger()
	}
	clientName := strings.TrimSpace(cfg.ClientName)
	if clientName == "" {
		clientName = defaultClientName
	}
	clientVersion := strings.TrimSpace(cfg.ClientVersion)
	if clientVersion == "" {
		clientVersion = defaultClientVersion
	}

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.WorkingDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("acp stdout pipe: %w", err)
	}
	cmd.Stderr = stderr

	l.Debug().
		Str("binary", cfg.Command[0]).
		Strs("args", cfg.Command[1:]).
		Str("cwd", cfg.WorkingDir).
		Msg("starting acp process")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start acp process: %w", err)
	}
	if cmd.Process != nil {
		l.Debug().Int("pid", cmd.Process.Pid).Msg("acp process started")
	}

	c := &Client{
		cmd:                  cmd,
		stdin:                stdin,
		permissionHandler:    cfg.PermissionHandler,
		onSessionNewSetModel: cfg.OnSessionNewSetModel,
		clientName:           clientName,
		clientVersion:        clientVersion,
		logger:               l,
		activeBySession:      make(map[acp.SessionId]*activePrompt),
		updates:              make(chan ExtendedSessionNotification, 256),
		deactivate:           make(chan acp.SessionId, 256),
		closed:               make(chan struct{}),
		dispatchDone:         make(chan struct{}),
	}

	wireWriter := newWireLoggingWriter(stdin, l)
	wireReader := newWireLoggingReader(stdout, l, c.enqueueUpdateFromWire)
	c.conn = acp.NewClientSideConnection(c, wireWriter, wireReader)

	go c.dispatchUpdates()
	go c.waitLoop()
	return c, nil
}

// Initialize performs ACP protocol initialization and validates protocol
// compatibility.
func (c *Client) Initialize(ctx context.Context) (acp.InitializeResponse, error) {
	c.logger.Debug().Msg("sending acp initialize")
	resp, err := c.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    c.clientName,
			Version: c.clientVersion,
		},
	})
	if err != nil {
		return acp.InitializeResponse{}, err
	}
	if resp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		return acp.InitializeResponse{}, fmt.Errorf("unsupported acp protocol version %d", resp.ProtocolVersion)
	}
	c.logger.Debug().Int("protocol_version", int(resp.ProtocolVersion)).Msg("acp initialize succeeded")
	return resp, nil
}

// Authenticate requests ACP authentication for a specific method.
func (c *Client) Authenticate(ctx context.Context, methodID string) error {
	if strings.TrimSpace(methodID) == "" {
		return nil
	}
	c.logger.Debug().Str("method_id", methodID).Msg("sending acp authenticate")
	_, err := c.conn.Authenticate(ctx, acp.AuthenticateRequest{MethodId: acp.AuthMethodId(methodID)})
	if err != nil {
		return err
	}
	c.logger.Debug().Str("method_id", methodID).Msg("acp authenticate succeeded")
	return nil
}

// NewSession creates a new ACP session in the provided working directory.
func (c *Client) NewSession(ctx context.Context, cwd string) (acp.NewSessionResponse, error) {
	c.logger.Debug().Str("cwd", cwd).Msg("sending acp session/new")
	resp, err := c.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		return acp.NewSessionResponse{}, fmt.Errorf("acp session id is empty")
	}
	c.logger.Debug().Str("session_id", string(resp.SessionId)).Msg("acp session/new succeeded")
	return resp, nil
}

// CreateSession creates a new ACP session and applies a configured session
// model when requested.
func (c *Client) CreateSession(ctx context.Context, cwd, model string) (acp.NewSessionResponse, error) {
	resp, err := c.NewSession(ctx, cwd)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}

	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" || !c.onSessionNewSetModel {
		return resp, nil
	}
	if err := c.SetSessionModel(ctx, string(resp.SessionId), trimmedModel); err != nil {
		// Some ACP implementations (like Gemini ACP) might not support session/set_model
		// but instead require the model to be specified during session creation if at all.
		// If the method is not found, we ignore the error and proceed, as the session
		// was already created successfully.
		var acpErr *acp.Error
		if errors.As(err, &acpErr) && acpErr.Code == -32601 {
			c.logger.Debug().
				Str("session_id", string(resp.SessionId)).
				Str("model", trimmedModel).
				Msg("acp session/set_model not supported by server, ignoring")
		} else {
			return acp.NewSessionResponse{}, fmt.Errorf("set acp session model %q: %w", trimmedModel, err)
		}
	}
	if resp.Models != nil {
		resp.Models.CurrentModelId = acp.ModelId(trimmedModel)
	}
	return resp, nil
}

// SetSessionModel selects the active model for an ACP session.
func (c *Client) SetSessionModel(ctx context.Context, sessionID, model string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errSessionIDRequired
	}
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		return errModelRequired
	}

	c.logger.Debug().
		Str("session_id", sessionID).
		Str("model", trimmedModel).
		Msg("sending acp session/set_model")
	_, err := c.conn.SetSessionModel(ctx, acp.SetSessionModelRequest{
		SessionId: acp.SessionId(sessionID),
		ModelId:   acp.ModelId(trimmedModel),
	})
	if err != nil {
		return err
	}
	c.logger.Debug().
		Str("session_id", sessionID).
		Str("model", trimmedModel).
		Msg("acp session/set_model succeeded")
	return nil
}

// Prompt sends a prompt to an ACP session and streams session updates.
func (c *Client) Prompt(ctx context.Context, sessionID, prompt string) (<-chan ExtendedSessionNotification, <-chan PromptResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil, errSessionIDRequired
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, nil, errPromptRequired
	}

	c.stateMu.Lock()
	activeSessionID := acp.SessionId(sessionID)
	if c.activeBySession[activeSessionID] != nil {
		c.stateMu.Unlock()
		return nil, nil, ErrPromptAlreadyActive
	}
	updates := make(chan ExtendedSessionNotification, 64)
	active := &activePrompt{sessionID: activeSessionID, updates: updates, signal: make(chan struct{}, 1)}
	c.activeBySession[activeSessionID] = active
	c.stateMu.Unlock()

	c.logger.Debug().Str("session_id", sessionID).Int("prompt_len", len(prompt)).Msg("sending acp session/prompt")

	resultCh := make(chan PromptResult, 1)
	go func() {
		defer close(resultCh)
		defer c.clearActive(activeSessionID)

		resp, err := c.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: activeSessionID,
			Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
		})
		waitForUpdateIdle(ctx, active.signal)
		c.logLastChunkInSeries(activeSessionID)
		if err != nil {
			c.logger.Error().Err(err).Str("session_id", sessionID).Msg("acp session/prompt failed")
			resultCh <- PromptResult{Err: err}
			return
		}

		var usage map[string]any
		if meta, ok := resp.Meta.(map[string]any); ok {
			usage, _ = meta["usage"].(map[string]any)
		}
		c.logger.Debug().
			Str("session_id", sessionID).
			Str("stop_reason", string(resp.StopReason)).
			Interface("usage", usage).
			Msg("acp session/prompt completed")
		resultCh <- PromptResult{Response: resp, Usage: usage}
	}()


	return updates, resultCh, nil
}

// Close stops the ACP subprocess and waits for cleanup to finish.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.closed
	if c.closeErr != nil && !errors.Is(c.closeErr, io.EOF) {
		return c.closeErr
	}
	return nil
}

func (c *Client) waitLoop() {
	err := c.cmd.Wait()
	if err != nil {
		c.logger.Warn().Err(err).Msg("acp process exited with error")
		c.failAll(fmt.Errorf("acp process exit: %w", err))
		return
	}
	c.logger.Debug().Msg("acp process exited")
	c.failAll(io.EOF)
}

// RequestPermission handles ACP permission callbacks.
func (c *Client) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	c.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("title", title).
		Int("option_count", len(params.Options)).
		Msg("received acp permission request")

	if c.permissionHandler != nil {
		resp, err := c.permissionHandler(ctx, params)
		if err != nil {
			c.logger.Error().Err(err).Str("session_id", string(params.SessionId)).Msg("permission handler failed")
			return acp.RequestPermissionResponse{}, err
		}
		c.logger.Debug().
			Str("session_id", string(params.SessionId)).
			Str("outcome", permissionOutcomeLabel(resp.Outcome)).
			Msg("permission handler returned")
		return resp, nil
	}

	for _, option := range params.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			resp := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}
			c.logger.Debug().
				Str("session_id", string(params.SessionId)).
				Str("option_id", string(option.OptionId)).
				Str("option_kind", string(option.Kind)).
				Msg("permission auto-selected reject option")
			return resp, nil
		}
	}

	resp := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	c.logger.Debug().Str("session_id", string(params.SessionId)).Msg("permission auto-cancelled")
	return resp, nil
}

// SessionUpdate is part of the ACP client callback contract.
func (c *Client) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	logEvent := c.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("update_kind", sessionUpdateKind(params.Update))
	logACPUpdateContentFields(logEvent, params.Update)
	logACPUpdateChunkFields(logEvent, params.Update)
	logEvent.Msg("received acp session update callback")
	return nil
}

// ReadTextFile reports unsupported file read for this ACP client.
func (c *Client) ReadTextFile(_ context.Context, _ acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

// WriteTextFile reports unsupported file write for this ACP client.
func (c *Client) WriteTextFile(_ context.Context, _ acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

// CreateTerminal reports unsupported terminal creation for this ACP client.
func (c *Client) CreateTerminal(_ context.Context, _ acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

// KillTerminalCommand reports unsupported terminal command control for this ACP client.
func (c *Client) KillTerminalCommand(_ context.Context, _ acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

// TerminalOutput reports unsupported terminal output streaming for this ACP client.
func (c *Client) TerminalOutput(_ context.Context, _ acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

// ReleaseTerminal reports unsupported terminal release for this ACP client.
func (c *Client) ReleaseTerminal(_ context.Context, _ acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

// WaitForTerminalExit reports unsupported terminal wait operations for this ACP client.
func (c *Client) WaitForTerminalExit(_ context.Context, _ acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}

func (c *Client) clearActive(sessionID acp.SessionId) {
	select {
	case c.deactivate <- sessionID:
	case <-c.closed:
		<-c.dispatchDone
		c.closeActiveSession(sessionID)
	}
}

func (c *Client) logLastChunkInSeries(sessionID acp.SessionId) {
	c.stateMu.Lock()
	active := c.activeBySession[sessionID]
	var last *loggedACPChunk
	if active != nil && active.lastChunk != nil {
		chunkCopy := *active.lastChunk
		last = &chunkCopy
	}
	c.stateMu.Unlock()
	if last == nil {
		return
	}

	logEvent := c.logger.Debug().
		Str("session_id", string(sessionID)).
		Str("update_kind", last.kind).
		Bool("partial", last.partial).
		Bool("thought", last.thought).
		Bool("last_in_series", true)
	if last.contentBlock != nil {
		logEvent = logEvent.Interface("acp_content_block", last.contentBlock)
	}
	logEvent.Msg("received last acp chunk in series")
}

func (c *Client) failAll(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.closed)
		<-c.dispatchDone
		c.closeAllActiveSessions()
	})
}

func (c *Client) enqueueUpdateFromWire(ext ExtendedSessionNotification) {
	select {
	case c.updates <- ext:
	default:
		c.logger.Warn().Str("session_id", string(ext.SessionId)).Msg("dropping ordered wire update due to full buffer")
	}
}

func (c *Client) dispatchUpdates() {
	defer close(c.dispatchDone)
	for {
		select {
		case <-c.closed:
			return
		case sessionID := <-c.deactivate:
			c.closeActiveSession(sessionID)
		case ext := <-c.updates:
			c.dispatchSessionUpdate(ext)
		}
	}
}

func (c *Client) closeActiveSession(sessionID acp.SessionId) {
	c.stateMu.Lock()
	active := c.activeBySession[sessionID]
	delete(c.activeBySession, sessionID)
	c.stateMu.Unlock()
	if active != nil {
		active.closeOnce.Do(func() {
			close(active.updates)
		})
	}
}

func (c *Client) closeAllActiveSessions() {
	c.stateMu.Lock()
	active := make([]*activePrompt, 0, len(c.activeBySession))
	for sessionID, prompt := range c.activeBySession {
		active = append(active, prompt)
		delete(c.activeBySession, sessionID)
	}
	c.stateMu.Unlock()
	for _, prompt := range active {
		if prompt == nil {
			continue
		}
		prompt.closeOnce.Do(func() {
			close(prompt.updates)
		})
	}
}

func (c *Client) dispatchSessionUpdate(ext ExtendedSessionNotification) {
	updateType := sessionUpdateKind(ext.Update)
	logEvent := c.logger.Debug().
		Str("session_id", string(ext.SessionId)).
		Str("update_kind", updateType)

	if updateType == unknownValue {
		logEvent = logEvent.RawJSON("raw_update", ext.Raw)
	}

	logACPUpdateContentFields(logEvent, ext.Update)
	logACPUpdateChunkFields(logEvent, ext.Update)
	logEvent.Msg("received acp session update")

	c.stateMu.Lock()
	active := c.activeBySession[ext.SessionId]
	if active != nil {
		active.lastChunk = loggedACPChunkFromUpdate(ext.Update)
	}
	c.stateMu.Unlock()
	if active == nil {
		return
	}
	select {
	case active.updates <- ext:
		select {
		case active.signal <- struct{}{}:
		default:
		}
	case <-c.closed:
	}
}


func permissionOutcomeLabel(outcome acp.RequestPermissionOutcome) string {
	switch {
	case outcome.Selected != nil:
		return "selected"
	case outcome.Cancelled != nil:
		return "cancelled"
	default:
		return unknownValue
	}
}

func sessionUpdateKind(update acp.SessionUpdate) string {
	switch {
	case update.AgentMessageChunk != nil:
		return "agent_message_chunk"
	case update.UserMessageChunk != nil:
		return "user_message_chunk"
	case update.AgentThoughtChunk != nil:
		return "agent_thought_chunk"
	case update.ToolCall != nil:
		return "tool_call"
	case update.ToolCallUpdate != nil:
		return "tool_call_update"
	case update.Plan != nil:
		return "plan"
	case update.AvailableCommandsUpdate != nil:
		return "available_commands_update"
	case update.CurrentModeUpdate != nil:
		return "current_mode_update"
	default:
		return unknownValue
	}
}

func logACPUpdateContentFields(event *zerolog.Event, update acp.SessionUpdate) {
	if event == nil {
		return
	}
	switch {
	case update.AgentMessageChunk != nil:
		event.Interface("acp_content_block", acpContentBlockLogValue(update.AgentMessageChunk.Content))
	case update.UserMessageChunk != nil:
		event.Interface("acp_content_block", acpContentBlockLogValue(update.UserMessageChunk.Content))
	case update.AgentThoughtChunk != nil:
		event.Interface("acp_content_block", acpContentBlockLogValue(update.AgentThoughtChunk.Content))
	}
}

func logACPUpdateChunkFields(event *zerolog.Event, update acp.SessionUpdate) {
	if event == nil {
		return
	}
	switch {
	case update.AgentMessageChunk != nil:
		event.Bool("partial", true).Bool("thought", false)
	case update.AgentThoughtChunk != nil:
		event.Bool("partial", true).Bool("thought", true)
	case update.UserMessageChunk != nil:
		event.Bool("partial", true).Bool("thought", false)
	}
}

func loggedACPChunkFromUpdate(update acp.SessionUpdate) *loggedACPChunk {
	switch {
	case update.AgentMessageChunk != nil:
		return &loggedACPChunk{
			kind:         "agent_message_chunk",
			contentBlock: acpContentBlockLogValue(update.AgentMessageChunk.Content),
			partial:      true,
			thought:      false,
		}
	case update.AgentThoughtChunk != nil:
		return &loggedACPChunk{
			kind:         "agent_thought_chunk",
			contentBlock: acpContentBlockLogValue(update.AgentThoughtChunk.Content),
			partial:      true,
			thought:      true,
		}
	case update.UserMessageChunk != nil:
		return &loggedACPChunk{
			kind:         "user_message_chunk",
			contentBlock: acpContentBlockLogValue(update.UserMessageChunk.Content),
			partial:      true,
			thought:      false,
		}
	default:
		return nil
	}
}

func waitForUpdateIdle(ctx context.Context, signal <-chan struct{}) {
	timer := time.NewTimer(idleUpdateWindow)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-signal:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleUpdateWindow)
		case <-timer.C:
			return
		}
	}
}

type wireLoggingWriter struct {
	writer io.Writer
	buffer *wireLogBuffer
}

func newWireLoggingWriter(writer io.Writer, logger zerolog.Logger) io.Writer {
	return &wireLoggingWriter{writer: writer, buffer: newWireLogBuffer("send", logger, nil)}
}

func (w *wireLoggingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.buffer.append(p[:n])
	}
	if err != nil {
		w.buffer.logger.Warn().Err(err).Msg("failed to write acp stream")
	}
	return n, err
}

type wireLoggingReader struct {
	reader io.Reader
	buffer *wireLogBuffer
}

func newWireLoggingReader(reader io.Reader, logger zerolog.Logger, onSessionUpdate func(ExtendedSessionNotification)) io.Reader {
	return &wireLoggingReader{reader: reader, buffer: newWireLogBuffer("recv", logger, onSessionUpdate)}
}

func (r *wireLoggingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.buffer.append(p[:n])
	}
	if err != nil && !errors.Is(err, io.EOF) {
		r.buffer.logger.Warn().Err(err).Msg("failed to read acp stream")
	}
	return n, err
}

type wireLogBuffer struct {
	direction string
	logger    zerolog.Logger
	onUpdate  func(ExtendedSessionNotification)

	mu  sync.Mutex
	buf []byte
}

func newWireLogBuffer(direction string, logger zerolog.Logger, onUpdate func(ExtendedSessionNotification)) *wireLogBuffer {
	return &wireLogBuffer{direction: direction, logger: logger, onUpdate: onUpdate}
}

func (b *wireLogBuffer) append(chunk []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, chunk...)
	for {
		idx := bytes.IndexByte(b.buf, '\n')
		if idx < 0 {
			return
		}
		line := bytes.TrimSpace(b.buf[:idx])
		b.buf = b.buf[idx+1:]
		if len(line) == 0 {
			continue
		}
		b.logLine(line)
	}
}

func (b *wireLogBuffer) logLine(line []byte) {
	type wireEnvelope struct {
		Method string          `json:"method,omitempty"`
		ID     json.RawMessage `json:"id,omitempty"`
		Params json.RawMessage `json:"params,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	var env wireEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		b.logger.Warn().
			Str("direction", b.direction).
			Err(err).
			Msg("failed to decode acp wire payload")
		return
	}

	if b.direction == "recv" && env.Method == acp.ClientMethodSessionUpdate && b.onUpdate != nil && len(env.Params) > 0 {
		var note acp.SessionNotification
		if err := json.Unmarshal(env.Params, &note); err == nil {
			b.onUpdate(ExtendedSessionNotification{
				SessionNotification: note,
				Raw:                 env.Params,
			})
		} else {
			b.logger.Warn().Err(err).Msg("failed to decode ordered session update")
		}
	}

	kind := unknownValue
	switch {
	case env.Method != "" && len(env.ID) > 0:
		kind = "request"
	case env.Method != "":
		kind = "notification"
	case len(env.ID) > 0:
		kind = "response"
	}

	evt := b.logger.Debug().
		Str("direction", b.direction).
		Str("rpc_kind", kind)
	if env.Method != "" {
		evt = evt.Str("method", env.Method)
	}
	if len(env.ID) > 0 {
		evt = evt.Str("id", strings.TrimSpace(string(env.ID)))
	}
	if len(env.Params) > 0 {
		evt = evt.RawJSON("params", env.Params)
	}
	if len(env.Result) > 0 {
		evt = evt.RawJSON("result", env.Result)
	}
	if env.Error != nil {
		evt = evt.Int("error_code", env.Error.Code).Str("error_message", env.Error.Message)
	}
	evt.Msg("acp wire")
}

