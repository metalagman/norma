package codexacp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

const (
	codexToolName      = "codex"
	codexReplyToolName = "codex-reply"
)

func TestBuildCodexMCPCommand(t *testing.T) {
	got := buildCodexMCPCommand(Options{})
	want := []string{"codex", "mcp-server"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildCodexMCPCommand() = %v, want %v", got, want)
	}
}

func TestBuildCodexMCPCommandDoesNotInjectModelConfig(t *testing.T) {
	got := buildCodexMCPCommand(Options{
		CodexModel: "gpt-5.4",
	})
	want := []string{"codex", "mcp-server"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildCodexMCPCommand() = %v, want %v", got, want)
	}
}

func TestRunProxyRejectsInvalidCodexSandbox(t *testing.T) {
	err := RunProxy(
		context.Background(),
		t.TempDir(),
		Options{CodexSandbox: "invalid"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("RunProxy() error = nil, want invalid codex sandbox error")
	}
	if !strings.Contains(err.Error(), "invalid codex sandbox") {
		t.Fatalf("RunProxy() error = %q, want invalid codex sandbox", err.Error())
	}
}

func TestBuildCodexToolInvocationIncludesCodexConfigOnInitialCall(t *testing.T) {
	toolName, args := buildCodexToolInvocation(
		"",
		"/tmp/work",
		"hello",
		codexToolConfig{
			ApprovalPolicy:        "on-request",
			BaseInstructions:      "base",
			CompactPrompt:         "compact",
			Config:                map[string]any{"foo": "bar"},
			DeveloperInstructions: "dev",
			Model:                 "gpt-5.2-codex",
			Profile:               "team",
			Sandbox:               "workspace-write",
		},
		"",
	)
	if toolName != codexToolName {
		t.Fatalf("toolName = %q, want %q", toolName, codexToolName)
	}
	if got := mapArgString(args, "prompt"); got != "hello" {
		t.Fatalf("prompt = %q, want %q", got, "hello")
	}
	if got := mapArgString(args, "cwd"); got != "/tmp/work" {
		t.Fatalf("cwd = %q, want %q", got, "/tmp/work")
	}
	if got := mapArgString(args, "approval-policy"); got != "on-request" {
		t.Fatalf("approval-policy = %q, want %q", got, "on-request")
	}
	if got := mapArgString(args, "base-instructions"); got != "base" {
		t.Fatalf("base-instructions = %q, want %q", got, "base")
	}
	if got := mapArgString(args, "compact-prompt"); got != "compact" {
		t.Fatalf("compact-prompt = %q, want %q", got, "compact")
	}
	if got := mapArgString(args, "developer-instructions"); got != "dev" {
		t.Fatalf("developer-instructions = %q, want %q", got, "dev")
	}
	if got := mapArgString(args, "model"); got != "gpt-5.2-codex" {
		t.Fatalf("model = %q, want %q", got, "gpt-5.2-codex")
	}
	if got := mapArgString(args, "profile"); got != "team" {
		t.Fatalf("profile = %q, want %q", got, "team")
	}
	if got := mapArgString(args, "sandbox"); got != "workspace-write" {
		t.Fatalf("sandbox = %q, want %q", got, "workspace-write")
	}
	cfgArg, ok := args["config"].(map[string]any)
	if !ok {
		t.Fatalf("config type = %T, want map[string]any", args["config"])
	}
	if got, ok := cfgArg["foo"].(string); !ok || got != "bar" {
		t.Fatalf("config.foo = %v, want %q", cfgArg["foo"], "bar")
	}
}

func TestBuildCodexToolInvocationReplyOmitsCodexConfig(t *testing.T) {
	toolName, args := buildCodexToolInvocation(
		"thread-1",
		"/tmp/work",
		"hello",
		codexToolConfig{
			Model:   "gpt-5.2-codex",
			Sandbox: "workspace-write",
		},
		"",
	)
	if toolName != codexReplyToolName {
		t.Fatalf("toolName = %q, want %q", toolName, codexReplyToolName)
	}
	if got := mapArgString(args, "threadId"); got != "thread-1" {
		t.Fatalf("threadId = %q, want %q", got, "thread-1")
	}
	if got := mapArgString(args, "prompt"); got != "hello" {
		t.Fatalf("prompt = %q, want %q", got, "hello")
	}
	if _, ok := args["model"]; ok {
		t.Fatalf("reply args unexpectedly contain model: %v", args)
	}
	if _, ok := args["sandbox"]; ok {
		t.Fatalf("reply args unexpectedly contain sandbox: %v", args)
	}
	if _, ok := args["cwd"]; ok {
		t.Fatalf("reply args unexpectedly contain cwd: %v", args)
	}
}

func TestBuildCodexToolInvocationSessionModelOverridesConfiguredModel(t *testing.T) {
	toolName, args := buildCodexToolInvocation(
		"",
		"",
		"hello",
		codexToolConfig{Model: "gpt-default"},
		"gpt-session",
	)
	if toolName != codexToolName {
		t.Fatalf("toolName = %q, want %q", toolName, codexToolName)
	}
	if got := mapArgString(args, "model"); got != "gpt-session" {
		t.Fatalf("model = %q, want %q", got, "gpt-session")
	}
}

func TestSessionUpdateType(t *testing.T) {
	t.Run("tool call update", func(t *testing.T) {
		update := acp.UpdateToolCall(acp.ToolCallId("call-1"), acp.WithUpdateStatus(acp.ToolCallStatusCompleted))
		if got := sessionUpdateType(update); got != "tool_call_update" {
			t.Fatalf("sessionUpdateType() = %q, want %q", got, "tool_call_update")
		}
	})

	t.Run("agent message chunk", func(t *testing.T) {
		update := acp.UpdateAgentMessageText("hello")
		if got := sessionUpdateType(update); got != "agent_message_chunk" {
			t.Fatalf("sessionUpdateType() = %q, want %q", got, "agent_message_chunk")
		}
	})
}

func TestSessionUpdatePayloadIncludesDiscriminator(t *testing.T) {
	update := acp.UpdateToolCall(acp.ToolCallId("call-1"), acp.WithUpdateStatus(acp.ToolCallStatusCompleted))
	payload := sessionUpdatePayload(update)
	if !strings.Contains(payload, `"sessionUpdate":"tool_call_update"`) {
		t.Fatalf("sessionUpdatePayload() = %q, want sessionUpdate discriminator", payload)
	}
}

func TestExtractCodexToolResultPrefersStructuredContentText(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "content channel response"},
		},
		StructuredContent: map[string]any{
			"threadId": "thread-123",
			"content":  "structured response",
		},
	}

	threadID, text := extractCodexToolResult(result)
	if threadID != "thread-123" {
		t.Fatalf("threadID = %q, want %q", threadID, "thread-123")
	}
	if text != "structured response" {
		t.Fatalf("text = %q, want %q", text, "structured response")
	}
}

func TestCodexACPProxyPromptUsesCodexThenReply(t *testing.T) {
	fakeSession := &fakeCodexMCPToolSession{
		listTools: []*mcp.Tool{
			{Name: codexToolName},
			{Name: codexReplyToolName},
		},
		callResults: []*mcp.CallToolResult{
			{
				StructuredContent: map[string]any{
					"threadId": "thread-1",
					"content":  "first response",
				},
			},
			{
				StructuredContent: map[string]any{
					"threadId": "thread-1",
					"content":  "second response",
				},
			},
		},
	}
	updater := &fakeACPSessionUpdater{}
	agent := newCodexACPProxyAgent(fakeSession, "test-agent", codexToolConfig{}, zerolog.Nop())
	agent.setConnection(updater)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("first prompt")},
	}); err != nil {
		t.Fatalf("first Prompt() error = %v", err)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("second prompt")},
	}); err != nil {
		t.Fatalf("second Prompt() error = %v", err)
	}

	calls := fakeSession.callsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("len(calls) = %d, want 2", len(calls))
	}
	if calls[0].Name != codexToolName {
		t.Fatalf("calls[0].Name = %q, want %q", calls[0].Name, codexToolName)
	}
	if got := mapArgString(calls[0].Arguments, "prompt"); got != "first prompt" {
		t.Fatalf("first call prompt = %q, want %q", got, "first prompt")
	}
	if got := mapArgString(calls[0].Arguments, "cwd"); got != "/tmp/work" {
		t.Fatalf("first call cwd = %q, want %q", got, "/tmp/work")
	}

	if calls[1].Name != codexReplyToolName {
		t.Fatalf("calls[1].Name = %q, want %q", calls[1].Name, codexReplyToolName)
	}
	if got := mapArgString(calls[1].Arguments, "prompt"); got != "second prompt" {
		t.Fatalf("second call prompt = %q, want %q", got, "second prompt")
	}
	if got := mapArgString(calls[1].Arguments, "threadId"); got != "thread-1" {
		t.Fatalf("second call threadId = %q, want %q", got, "thread-1")
	}

	textUpdates := updater.agentMessageTexts(newResp.SessionId)
	if len(textUpdates) != 2 {
		t.Fatalf("len(agent message updates) = %d, want 2", len(textUpdates))
	}
	if textUpdates[0] != "first response" || textUpdates[1] != "second response" {
		t.Fatalf("agent message updates = %v, want [first response second response]", textUpdates)
	}
}

func TestCodexACPProxyCancelStopsPrompt(t *testing.T) {
	started := make(chan struct{})
	fakeSession := &fakeCodexMCPToolSession{
		listTools: []*mcp.Tool{
			{Name: codexToolName},
			{Name: codexReplyToolName},
		},
		callHook: func(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	updater := &fakeACPSessionUpdater{}
	agent := newCodexACPProxyAgent(fakeSession, "test-agent", codexToolConfig{}, zerolog.Nop())
	agent.setConnection(updater)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	type promptResult struct {
		resp acp.PromptResponse
		err  error
	}
	promptDone := make(chan promptResult, 1)
	go func() {
		resp, promptErr := agent.Prompt(context.Background(), acp.PromptRequest{
			SessionId: newResp.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("please block")},
		})
		promptDone <- promptResult{resp: resp, err: promptErr}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt start")
	}

	if err := agent.Cancel(context.Background(), acp.CancelNotification{SessionId: newResp.SessionId}); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	select {
	case got := <-promptDone:
		if got.err != nil {
			t.Fatalf("Prompt() error = %v", got.err)
		}
		if got.resp.StopReason != acp.StopReasonCancelled {
			t.Fatalf("StopReason = %q, want %q", got.resp.StopReason, acp.StopReasonCancelled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled prompt")
	}
}

func TestCodexACPProxyInitializeUsesConfiguredAgentName(t *testing.T) {
	agent := newCodexACPProxyAgent(&fakeCodexMCPToolSession{}, "team-codex", codexToolConfig{}, zerolog.Nop())
	resp, err := agent.Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentInfo == nil {
		t.Fatalf("AgentInfo is nil")
	}
	if resp.AgentInfo.Name != "team-codex" {
		t.Fatalf("AgentInfo.Name = %q, want %q", resp.AgentInfo.Name, "team-codex")
	}
}

func TestCodexACPProxyInitializeUsesDefaultAgentNameWhenEmpty(t *testing.T) {
	agent := newCodexACPProxyAgent(&fakeCodexMCPToolSession{}, "", codexToolConfig{}, zerolog.Nop())
	resp, err := agent.Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentInfo == nil {
		t.Fatalf("AgentInfo is nil")
	}
	if resp.AgentInfo.Name != DefaultAgentName {
		t.Fatalf("AgentInfo.Name = %q, want %q", resp.AgentInfo.Name, DefaultAgentName)
	}
}

func TestRunProxyStartsCodexMCPServer(t *testing.T) {
	wrapper, argsFile := writeCodexMCPWrapper(t)
	codexDir := t.TempDir()
	codexPath := filepath.Join(codexDir, "codex")

	wrapperContent, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", wrapper, err)
	}
	if err := os.WriteFile(codexPath, wrapperContent, 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", codexPath, err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", codexDir+string(os.PathListSeparator)+originalPath)

	var stderr bytes.Buffer
	runErr := RunProxy(
		context.Background(),
		t.TempDir(),
		Options{},
		strings.NewReader(""),
		io.Discard,
		&stderr,
	)
	if runErr != nil {
		t.Fatalf("RunProxy() error = %v; stderr=%s", runErr, stderr.String())
	}

	args := readArgsFile(t, argsFile)
	for _, want := range []string{"mcp-server"} {
		if !containsArg(args, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
	if containsArg(args, "--trace") {
		t.Fatalf("args %v unexpectedly contain passthrough argument %q", args, "--trace")
	}
}

func TestProxyMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PROXY_MCP_HELPER") != "1" {
		return
	}
	mustHelper(runProxyMCPHelper(context.Background()))
	os.Exit(0)
}

func runProxyMCPHelper(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "proxy-mcp-helper", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: codexToolName, Description: "Starts a codex thread"}, func(_ context.Context, _ *mcp.CallToolRequest, input proxyCodexToolInput) (*mcp.CallToolResult, proxyCodexToolOutput, error) {
		return nil, proxyCodexToolOutput{
			ThreadID: "thread-test",
			Content:  "codex:" + input.Prompt,
		}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: codexReplyToolName, Description: "Continues a codex thread"}, func(_ context.Context, _ *mcp.CallToolRequest, input proxyCodexReplyInput) (*mcp.CallToolResult, proxyCodexToolOutput, error) {
		return nil, proxyCodexToolOutput{
			ThreadID: input.ThreadID,
			Content:  "reply:" + input.Prompt,
		}, nil
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}

func writeCodexMCPWrapper(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	wrapperPath := filepath.Join(dir, "codex-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
: > %s
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %s
done
exec env GO_WANT_PROXY_MCP_HELPER=1 %s -test.run=TestProxyMCPHelperProcess -- "$@"
`, shellQuote(argsFile), shellQuote(argsFile), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath, argsFile
}

func readArgsFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func mapArgString(v any, key string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

type fakeCodexToolCall struct {
	Name      string
	Arguments any
}

type fakeCodexMCPToolSession struct {
	mu sync.Mutex

	listTools   []*mcp.Tool
	callResults []*mcp.CallToolResult
	callHook    func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	closeErr    error
	waitErr     error
	calls       []fakeCodexToolCall
}

func (s *fakeCodexMCPToolSession) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	if params != nil {
		s.calls = append(s.calls, fakeCodexToolCall{Name: params.Name, Arguments: params.Arguments})
	}
	hook := s.callHook
	var result *mcp.CallToolResult
	if len(s.callResults) > 0 {
		result = s.callResults[0]
		s.callResults = s.callResults[1:]
	}
	s.mu.Unlock()

	if hook != nil {
		return hook(ctx, params)
	}
	if result != nil {
		return result, nil
	}
	return &mcp.CallToolResult{}, nil
}

func (s *fakeCodexMCPToolSession) ListTools(_ context.Context, _ *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &mcp.ListToolsResult{Tools: append([]*mcp.Tool(nil), s.listTools...)}, nil
}

func (s *fakeCodexMCPToolSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func (s *fakeCodexMCPToolSession) Wait() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

func (s *fakeCodexMCPToolSession) callsSnapshot() []fakeCodexToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeCodexToolCall, len(s.calls))
	copy(out, s.calls)
	return out
}

type fakeACPSessionUpdater struct {
	mu      sync.Mutex
	updates []acp.SessionNotification
}

func (u *fakeACPSessionUpdater) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.updates = append(u.updates, params)
	return nil
}

func (u *fakeACPSessionUpdater) agentMessageTexts(sessionID acp.SessionId) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, 0)
	for _, update := range u.updates {
		if update.SessionId != sessionID {
			continue
		}
		if update.Update.AgentMessageChunk == nil {
			continue
		}
		if update.Update.AgentMessageChunk.Content.Text == nil {
			continue
		}
		text := update.Update.AgentMessageChunk.Content.Text.Text
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

type proxyCodexToolInput struct {
	Prompt string `json:"prompt"`
}

type proxyCodexReplyInput struct {
	ThreadID string `json:"threadId"`
	Prompt   string `json:"prompt"`
}

type proxyCodexToolOutput struct {
	ThreadID string `json:"threadId"`
	Content  string `json:"content"`
}

func mustHelper(err error) {
	if err != nil {
		panic(err)
	}
}
