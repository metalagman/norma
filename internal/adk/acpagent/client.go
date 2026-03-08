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
	"github.com/rs/zerolog/log"
)

var errPromptAlreadyActive = errors.New("acp prompt already active")

const unknownValue = "unknown"

type PermissionHandler func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)

type ClientConfig struct {
	Command           []string
	WorkingDir        string
	Stderr            io.Writer
	PermissionHandler PermissionHandler
	Logger            *zerolog.Logger
}

type Client struct {
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	conn              *acp.ClientSideConnection
	permissionHandler PermissionHandler
	logger            zerolog.Logger

	stateMu sync.Mutex
	active  *activePrompt
	updates chan acp.SessionNotification

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type activePrompt struct {
	sessionID acp.SessionId
	updates   chan acp.SessionNotification
	signal    chan struct{}
}

type PromptResult struct {
	Response acp.PromptResponse
	Err      error
}

var _ acp.Client = (*Client)(nil)

func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("acp command is required")
	}

	l := log.With().Str("component", "acpagent.client").Logger()
	if cfg.Logger != nil {
		l = cfg.Logger.With().Str("subcomponent", "acpagent.client").Logger()
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

	l.Info().
		Str("binary", cfg.Command[0]).
		Strs("args", cfg.Command[1:]).
		Str("cwd", cfg.WorkingDir).
		Msg("starting acp process")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start acp process: %w", err)
	}
	if cmd.Process != nil {
		l.Info().Int("pid", cmd.Process.Pid).Msg("acp process started")
	}

	c := &Client{
		cmd:               cmd,
		stdin:             stdin,
		permissionHandler: cfg.PermissionHandler,
		logger:            l,
		updates:           make(chan acp.SessionNotification, 256),
		closed:            make(chan struct{}),
	}

	wireWriter := newWireLoggingWriter(stdin, l)
	wireReader := newWireLoggingReader(stdout, l, c.enqueueUpdateFromWire)
	c.conn = acp.NewClientSideConnection(c, wireWriter, wireReader)

	go c.dispatchUpdates()
	go c.waitLoop()
	return c, nil
}

func (c *Client) Initialize(ctx context.Context) (acp.InitializeResponse, error) {
	c.logger.Info().Msg("sending acp initialize")
	resp, err := c.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "norma-playground",
			Version: "dev",
		},
	})
	if err != nil {
		return acp.InitializeResponse{}, err
	}
	if resp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		return acp.InitializeResponse{}, fmt.Errorf("unsupported acp protocol version %d", resp.ProtocolVersion)
	}
	c.logger.Info().Int("protocol_version", int(resp.ProtocolVersion)).Msg("acp initialize succeeded")
	return resp, nil
}

func (c *Client) Authenticate(ctx context.Context, methodID string) error {
	if strings.TrimSpace(methodID) == "" {
		return nil
	}
	c.logger.Info().Str("method_id", methodID).Msg("sending acp authenticate")
	_, err := c.conn.Authenticate(ctx, acp.AuthenticateRequest{MethodId: acp.AuthMethodId(methodID)})
	if err != nil {
		return err
	}
	c.logger.Info().Str("method_id", methodID).Msg("acp authenticate succeeded")
	return nil
}

func (c *Client) NewSession(ctx context.Context, cwd string) (acp.NewSessionResponse, error) {
	c.logger.Info().Str("cwd", cwd).Msg("sending acp session/new")
	resp, err := c.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		return acp.NewSessionResponse{}, fmt.Errorf("acp session id is empty")
	}
	c.logger.Info().Str("session_id", string(resp.SessionId)).Msg("acp session/new succeeded")
	return resp, nil
}

func (c *Client) Prompt(ctx context.Context, sessionID, prompt string) (<-chan acp.SessionNotification, <-chan PromptResult, error) {
	c.stateMu.Lock()
	if c.active != nil {
		c.stateMu.Unlock()
		return nil, nil, errPromptAlreadyActive
	}
	updates := make(chan acp.SessionNotification, 64)
	activeSessionID := acp.SessionId(sessionID)
	active := &activePrompt{sessionID: activeSessionID, updates: updates, signal: make(chan struct{}, 1)}
	c.active = active
	c.stateMu.Unlock()

	c.logger.Info().Str("session_id", sessionID).Int("prompt_len", len(prompt)).Msg("sending acp session/prompt")

	resultCh := make(chan PromptResult, 1)
	go func() {
		defer close(resultCh)
		defer close(updates)
		defer c.clearActive(activeSessionID)

		resp, err := c.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: activeSessionID,
			Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
		})
		waitForUpdateIdle(ctx, active.signal)
		if err != nil {
			c.logger.Error().Err(err).Str("session_id", sessionID).Msg("acp session/prompt failed")
			resultCh <- PromptResult{Err: err}
			return
		}
		c.logger.Info().
			Str("session_id", sessionID).
			Str("stop_reason", string(resp.StopReason)).
			Msg("acp session/prompt completed")
		resultCh <- PromptResult{Response: resp}
	}()

	return updates, resultCh, nil
}

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
	c.logger.Info().Msg("acp process exited")
	c.failAll(io.EOF)
}

func (c *Client) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	c.logger.Info().
		Str("session_id", string(params.SessionId)).
		Str("title", title).
		Int("option_count", len(params.Options)).
		Msg("received acp permission request")

	if c.permissionHandler != nil {
		resp, err := c.permissionHandler(context.Background(), params)
		if err != nil {
			c.logger.Error().Err(err).Str("session_id", string(params.SessionId)).Msg("permission handler failed")
			return acp.RequestPermissionResponse{}, err
		}
		c.logger.Info().
			Str("session_id", string(params.SessionId)).
			Str("outcome", permissionOutcomeLabel(resp.Outcome)).
			Msg("permission handler returned")
		return resp, nil
	}

	for _, option := range params.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			resp := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}
			c.logger.Info().
				Str("session_id", string(params.SessionId)).
				Str("option_id", string(option.OptionId)).
				Str("option_kind", string(option.Kind)).
				Msg("permission auto-selected reject option")
			return resp, nil
		}
	}

	resp := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	c.logger.Info().Str("session_id", string(params.SessionId)).Msg("permission auto-cancelled")
	return resp, nil
}

func (c *Client) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.logger.Debug().
		Str("session_id", string(params.SessionId)).
		Str("update_kind", sessionUpdateKind(params.Update)).
		Msg("received acp session update callback")
	return nil
}

func (c *Client) ReadTextFile(_ context.Context, _ acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (c *Client) WriteTextFile(_ context.Context, _ acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (c *Client) CreateTerminal(_ context.Context, _ acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (c *Client) KillTerminalCommand(_ context.Context, _ acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (c *Client) TerminalOutput(_ context.Context, _ acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (c *Client) ReleaseTerminal(_ context.Context, _ acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (c *Client) WaitForTerminalExit(_ context.Context, _ acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}

func (c *Client) clearActive(sessionID acp.SessionId) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.active != nil && c.active.sessionID == sessionID {
		c.active = nil
	}
}

func (c *Client) failAll(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.closed)
		c.stateMu.Lock()
		c.active = nil
		c.stateMu.Unlock()
	})
}

func (c *Client) enqueueUpdateFromWire(note acp.SessionNotification) {
	select {
	case c.updates <- note:
	default:
		c.logger.Warn().Str("session_id", string(note.SessionId)).Msg("dropping ordered wire update due to full buffer")
	}
}

func (c *Client) dispatchUpdates() {
	for {
		select {
		case <-c.closed:
			return
		case note := <-c.updates:
			c.dispatchSessionUpdate(note)
		}
	}
}

func (c *Client) dispatchSessionUpdate(note acp.SessionNotification) {
	c.logger.Info().
		Str("session_id", string(note.SessionId)).
		Str("update_kind", sessionUpdateKind(note.Update)).
		Msg("received acp session update")

	c.stateMu.Lock()
	active := c.active
	c.stateMu.Unlock()
	if active == nil || active.sessionID != note.SessionId {
		return
	}
	select {
	case active.updates <- note:
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

func waitForUpdateIdle(ctx context.Context, signal <-chan struct{}) {
	const idleWindow = 20 * time.Millisecond
	timer := time.NewTimer(idleWindow)
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
			timer.Reset(idleWindow)
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

func newWireLoggingReader(reader io.Reader, logger zerolog.Logger, onSessionUpdate func(acp.SessionNotification)) io.Reader {
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
	onUpdate  func(acp.SessionNotification)

	mu  sync.Mutex
	buf []byte
}

func newWireLogBuffer(direction string, logger zerolog.Logger, onUpdate func(acp.SessionNotification)) *wireLogBuffer {
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
			b.onUpdate(note)
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

	evt := b.logger.Info().
		Str("direction", b.direction).
		Str("rpc_kind", kind)
	if env.Method != "" {
		evt = evt.Str("method", env.Method)
	}
	if len(env.ID) > 0 {
		evt = evt.Str("id", strings.TrimSpace(string(env.ID)))
	}
	if env.Error != nil {
		evt = evt.Int("error_code", env.Error.Code).Str("error_message", env.Error.Message)
	}
	evt.Msg("acp wire")
}
