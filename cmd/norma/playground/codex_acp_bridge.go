package playgroundcmd

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
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

type codexACPBridgeOptions struct {
	CodexArgs []string
}

type codexACPBridgeAgent struct {
	mcpSession codexMCPToolSession

	connMu sync.RWMutex
	conn   codexACPSessionUpdater

	mu            sync.Mutex
	sessions      map[acp.SessionId]*codexBridgeSessionState
	nextSessionID uint64
	nextToolID    uint64
}

type codexBridgeSessionState struct {
	cwd    string
	thread string
	cancel context.CancelFunc
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

func codexACPBridgeCommand() *cobra.Command {
	opts := codexACPBridgeOptions{}
	return newACPPlaygroundCommand(
		"codex-acp-bridge",
		"Expose Codex MCP server as ACP over stdio",
		func(cmd *cobra.Command) {
			cmd.Flags().StringArrayVar(&opts.CodexArgs, "codex-arg", nil, "extra codex mcp-server argument (repeatable)")
		},
		func(ctx context.Context, repoRoot string, stdin io.Reader, stdout, stderr io.Writer) error {
			return runCodexACPBridge(ctx, repoRoot, opts, stdin, stdout, stderr)
		},
	)
}

func runCodexACPBridge(ctx context.Context, repoRoot string, opts codexACPBridgeOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	lockedStderr := &syncWriter{writer: stderr}
	command := buildCodexACPBridgeCommand(opts)

	mcpSession, err := connectCodexMCPBridgeSession(ctx, repoRoot, command, lockedStderr)
	if err != nil {
		return err
	}
	defer func() {
		_ = mcpSession.Close()
	}()

	if err := ensureCodexBridgeTools(ctx, mcpSession); err != nil {
		return err
	}

	bridge := newCodexACPBridgeAgent(mcpSession)
	conn := acp.NewAgentSideConnection(bridge, stdout, stdin)
	bridge.setConnection(conn)

	backendDone := make(chan error, 1)
	go func() {
		backendDone <- mcpSession.Wait()
	}()

	select {
	case <-conn.Done():
		_ = mcpSession.Close()
		_ = awaitBackendStop(backendDone, 2*time.Second)
		return nil
	case err := <-backendDone:
		if err == nil {
			return &exitCodeError{
				code: 1,
				err:  errors.New("codex mcp-server exited before ACP client disconnected"),
			}
		}
		code := extractExitCode(err)
		return &exitCodeError{
			code: code,
			err:  fmt.Errorf("codex mcp-server exited: %w", err),
		}
	case <-ctx.Done():
		_ = mcpSession.Close()
		_ = awaitBackendStop(backendDone, 2*time.Second)
		return ctx.Err()
	}
}

func buildCodexACPBridgeCommand(opts codexACPBridgeOptions) []string {
	command := make([]string, 0, 2+len(opts.CodexArgs))
	command = append(command, "codex", "mcp-server")
	command = append(command, opts.CodexArgs...)
	return command
}

func connectCodexMCPBridgeSession(ctx context.Context, repoRoot string, command []string, stderr io.Writer) (codexMCPToolSession, error) {
	if len(command) == 0 {
		return nil, errors.New("empty codex command")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "norma-codex-acp-bridge", Version: "v0.0.1"}, nil)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Stderr = stderr
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to mcp command: %w", err)
	}
	return session, nil
}

func ensureCodexBridgeTools(ctx context.Context, session codexMCPToolSession) error {
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list mcp tools: %w", err)
	}
	if toolsResult == nil || len(toolsResult.Tools) == 0 {
		return errors.New("mcp tools list is empty")
	}
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
	return nil
}

func newCodexACPBridgeAgent(mcpSession codexMCPToolSession) *codexACPBridgeAgent {
	return &codexACPBridgeAgent{
		mcpSession: mcpSession,
		sessions:   make(map[acp.SessionId]*codexBridgeSessionState),
	}
}

func (a *codexACPBridgeAgent) setConnection(conn codexACPSessionUpdater) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.conn = conn
}

func (a *codexACPBridgeAgent) Authenticate(_ context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *codexACPBridgeAgent) Initialize(_ context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    "norma-codex-acp-bridge",
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

func (a *codexACPBridgeAgent) Cancel(_ context.Context, params acp.CancelNotification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok := a.sessions[params.SessionId]
	if !ok || state.cancel == nil {
		return nil
	}
	state.cancel()
	return nil
}

func (a *codexACPBridgeAgent) NewSession(_ context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sessionID := acp.SessionId(fmt.Sprintf("session-%d", atomic.AddUint64(&a.nextSessionID, 1)))

	a.mu.Lock()
	a.sessions[sessionID] = &codexBridgeSessionState{
		cwd: strings.TrimSpace(params.Cwd),
	}
	a.mu.Unlock()

	return acp.NewSessionResponse{SessionId: sessionID}, nil
}

func (a *codexACPBridgeAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	userPrompt := strings.TrimSpace(joinPromptText(params.Prompt))
	if userPrompt == "" {
		return acp.PromptResponse{}, acp.NewInvalidParams("prompt must include at least one text block")
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
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	threadID := state.thread
	cwd := state.cwd
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if cur, exists := a.sessions[params.SessionId]; exists {
			cur.cancel = nil
		}
		a.mu.Unlock()
	}()

	toolID := acp.ToolCallId(fmt.Sprintf("codex-tool-%d", atomic.AddUint64(&a.nextToolID, 1)))
	toolName, toolArgs := buildCodexToolInvocation(threadID, cwd, userPrompt)
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
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		return acp.PromptResponse{}, fmt.Errorf("call mcp tool %q: %w", toolName, err)
	}

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
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *codexACPBridgeAgent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	_ = params
	return acp.SetSessionModeResponse{}, nil
}

func (a *codexACPBridgeAgent) sendUpdate(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) error {
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

func buildCodexToolInvocation(threadID, cwd, prompt string) (string, map[string]any) {
	args := map[string]any{
		"prompt": prompt,
	}
	trimmedCwd := strings.TrimSpace(cwd)
	if trimmedCwd != "" && threadID == "" {
		args["cwd"] = trimmedCwd
	}
	if strings.TrimSpace(threadID) == "" {
		return "codex", args
	}
	args["threadId"] = threadID
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
