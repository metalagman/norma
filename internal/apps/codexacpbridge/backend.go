package codexacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

type appServerRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *appServerRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("bridge backend rpc error (%d): %s", e.Code, e.Message)
}

type appServerEnvelope struct {
	Method string             `json:"method,omitempty"`
	ID     json.RawMessage    `json:"id,omitempty"`
	Params json.RawMessage    `json:"params,omitempty"`
	Result json.RawMessage    `json:"result,omitempty"`
	Error  *appServerRPCError `json:"error,omitempty"`
}

type appServerInitializeResponse struct {
	UserAgent string `json:"userAgent"`
}

type appServerThreadStartResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model,omitempty"`
}

type appServerTurnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type appServerRPCResponse struct {
	result json.RawMessage
	err    error
}

type appServerNotification struct {
	Method string
	Params json.RawMessage
}

type appServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type appServerEvent struct {
	Notification *appServerNotification
	Request      *appServerRequest
}

type appServerBackend struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	logger *zerolog.Logger

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan appServerRPCResponse
	nextID    uint64

	events chan appServerEvent
	done   chan struct{}

	finalizeOnce sync.Once
	waitErr      error
	closeOnce    sync.Once

	initializeResp appServerInitializeResponse
}

type appServerSession interface {
	InitializeResponse() appServerInitializeResponse
	Events() <-chan appServerEvent
	ThreadStart(ctx context.Context, params map[string]any) (appServerThreadStartResponse, error)
	TurnStart(ctx context.Context, params map[string]any) (appServerTurnStartResponse, error)
	TurnInterrupt(ctx context.Context, threadID string, turnID string) error
	RespondRequest(ctx context.Context, req *appServerRequest, result any) error
	RespondRequestError(ctx context.Context, req *appServerRequest, code int, message string, data any) error
	Close() error
	Wait() error
}

func connectAppServerBackend(
	ctx context.Context,
	workingDir string,
	sessionCWD string,
	command []string,
	clientName string,
	stderr io.Writer,
	logger *zerolog.Logger,
) (*appServerBackend, error) {
	if len(command) == 0 {
		return nil, errors.New("empty codex command")
	}
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = strings.TrimSpace(sessionCWD)
	if cmd.Dir == "" {
		cmd.Dir = workingDir
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = io.Discard
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("bridge backend stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("bridge backend stdout pipe: %w", err)
	}
	logger.Debug().
		Str("cwd", cmd.Dir).
		Str("cmd", command[0]).
		Strs("args", command[1:]).
		Msg("starting codex bridge backend")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start codex bridge backend: %w", err)
	}

	backend := &appServerBackend{
		cmd:     cmd,
		stdin:   stdin,
		logger:  logger,
		pending: make(map[string]chan appServerRPCResponse),
		events:  make(chan appServerEvent, 256),
		done:    make(chan struct{}),
	}
	go backend.readLoop(stdout)
	go backend.waitLoop()

	initializeResp, err := backend.initialize(ctx, clientName)
	if err != nil {
		_ = backend.Close()
		_ = backend.Wait()
		return nil, err
	}
	backend.initializeResp = initializeResp
	return backend, nil
}

func (b *appServerBackend) InitializeResponse() appServerInitializeResponse {
	return b.initializeResp
}

func (b *appServerBackend) Events() <-chan appServerEvent {
	return b.events
}

func (b *appServerBackend) ThreadStart(ctx context.Context, params map[string]any) (appServerThreadStartResponse, error) {
	var resp appServerThreadStartResponse
	if err := b.call(ctx, "thread/start", params, &resp); err != nil {
		return appServerThreadStartResponse{}, err
	}
	if strings.TrimSpace(resp.Thread.ID) == "" {
		return appServerThreadStartResponse{}, errors.New("thread/start returned empty thread id")
	}
	return resp, nil
}

func (b *appServerBackend) TurnStart(ctx context.Context, params map[string]any) (appServerTurnStartResponse, error) {
	var resp appServerTurnStartResponse
	if err := b.call(ctx, "turn/start", params, &resp); err != nil {
		return appServerTurnStartResponse{}, err
	}
	if strings.TrimSpace(resp.Turn.ID) == "" {
		return appServerTurnStartResponse{}, errors.New("turn/start returned empty turn id")
	}
	return resp, nil
}

func (b *appServerBackend) TurnInterrupt(ctx context.Context, threadID string, turnID string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	return b.call(ctx, "turn/interrupt", map[string]any{
		"threadId": strings.TrimSpace(threadID),
		"turnId":   strings.TrimSpace(turnID),
	}, nil)
}

func (b *appServerBackend) RespondRequest(ctx context.Context, req *appServerRequest, result any) error {
	if req == nil || len(req.ID) == 0 {
		return errors.New("request id is required")
	}
	return b.sendResponse(ctx, req.ID, result)
}

func (b *appServerBackend) RespondRequestError(ctx context.Context, req *appServerRequest, code int, message string, data any) error {
	if req == nil || len(req.ID) == 0 {
		return errors.New("request id is required")
	}
	return b.sendError(ctx, req.ID, code, message, data)
}

func (b *appServerBackend) initialize(ctx context.Context, clientName string) (appServerInitializeResponse, error) {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    strings.TrimSpace(clientName),
			"title":   "Norma Codex ACP Bridge",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	var resp appServerInitializeResponse
	if err := b.call(ctx, "initialize", params, &resp); err != nil {
		return appServerInitializeResponse{}, fmt.Errorf("initialize bridge backend: %w", err)
	}
	if err := b.sendNotification(ctx, "initialized", nil); err != nil {
		return appServerInitializeResponse{}, fmt.Errorf("send initialized notification: %w", err)
	}
	return resp, nil
}

func (b *appServerBackend) call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddUint64(&b.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendUint(nil, id, 10))
	key := canonicalRequestID(idRaw)
	respCh := make(chan appServerRPCResponse, 1)

	b.pendingMu.Lock()
	b.pending[key] = respCh
	b.pendingMu.Unlock()
	defer func() {
		b.pendingMu.Lock()
		delete(b.pending, key)
		b.pendingMu.Unlock()
	}()

	if err := b.sendRequest(ctx, idRaw, method, params); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("bridge backend stopped")
	case resp := <-respCh:
		if resp.err != nil {
			return resp.err
		}
		if out == nil {
			return nil
		}
		if len(resp.result) == 0 || string(resp.result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.result, out); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

func (b *appServerBackend) sendRequest(ctx context.Context, id json.RawMessage, method string, params any) error {
	payload := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendResponse(ctx context.Context, id json.RawMessage, result any) error {
	payload := map[string]any{
		"id":     id,
		"result": result,
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendError(ctx context.Context, id json.RawMessage, code int, message string, data any) error {
	errPayload := map[string]any{
		"code":    code,
		"message": message,
	}
	if data != nil {
		errPayload["data"] = data
	}
	payload := map[string]any{
		"id":    id,
		"error": errPayload,
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendNotification(ctx context.Context, method string, params any) error {
	payload := map[string]any{
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendJSON(ctx context.Context, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bridge backend payload: %w", err)
	}

	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("bridge backend stopped")
	default:
	}

	if _, err := b.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write bridge backend payload: %w", err)
	}
	if b.logger.Debug().Enabled() {
		b.logger.Debug().Str("payload", string(raw)).Msg("bridge backend send")
	}
	return nil
}

func (b *appServerBackend) readLoop(stdout io.Reader) {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if parseErr := b.handleIncomingLine(line); parseErr != nil {
				b.logger.Warn().Err(parseErr).Str("line", strings.TrimSpace(string(line))).Msg("invalid bridge backend message")
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			b.logger.Warn().Err(err).Msg("bridge backend read loop stopped")
			return
		}
	}
}

func (b *appServerBackend) handleIncomingLine(line []byte) error {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil
	}

	var env appServerEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return err
	}
	if b.logger.Debug().Enabled() {
		b.logger.Debug().
			Str("method", env.Method).
			Str("id", canonicalRequestID(env.ID)).
			Str("payload", trimmed).
			Msg("bridge backend recv")
	}

	switch {
	case env.Method != "" && len(env.ID) > 0:
		b.events <- appServerEvent{
			Request: &appServerRequest{
				ID:     env.ID,
				Method: env.Method,
				Params: env.Params,
			},
		}
	case env.Method != "":
		b.events <- appServerEvent{
			Notification: &appServerNotification{
				Method: env.Method,
				Params: env.Params,
			},
		}
	case len(env.ID) > 0:
		key := canonicalRequestID(env.ID)
		b.pendingMu.Lock()
		respCh := b.pending[key]
		b.pendingMu.Unlock()
		if respCh == nil {
			return nil
		}
		if env.Error != nil {
			respCh <- appServerRPCResponse{err: env.Error}
			return nil
		}
		respCh <- appServerRPCResponse{result: env.Result}
	default:
	}
	return nil
}

func (b *appServerBackend) waitLoop() {
	err := b.cmd.Wait()
	b.finalizeOnce.Do(func() {
		b.waitErr = err
		close(b.done)
		close(b.events)
		b.failPending(err)
	})
}

func (b *appServerBackend) failPending(waitErr error) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	backendErr := errors.New("bridge backend stopped")
	if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
		backendErr = fmt.Errorf("bridge backend exited: %w", waitErr)
	}
	for key, ch := range b.pending {
		ch <- appServerRPCResponse{err: backendErr}
		delete(b.pending, key)
	}
}

func (b *appServerBackend) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		if err := b.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			closeErr = err
		}
		if b.cmd.Process != nil {
			if err := b.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				if closeErr == nil {
					closeErr = err
				}
			}
		}
	})
	return closeErr
}

func (b *appServerBackend) Wait() error {
	<-b.done
	if b.waitErr == nil || errors.Is(b.waitErr, os.ErrProcessDone) {
		return nil
	}
	return b.waitErr
}

func canonicalRequestID(id json.RawMessage) string {
	return strings.TrimSpace(string(id))
}
