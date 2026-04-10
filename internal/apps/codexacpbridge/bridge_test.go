package codexacpbridge

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
	mcpTransportStdio  = "stdio"
	mcpTransportHTTP   = "http"
	testMCPVersion     = "0.115.0"
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
	ctx := context.Background()
	err := RunProxy(
		ctx,
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

func TestRunProxyRejectsNilStreams(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()

	testCases := []struct {
		name   string
		stdin  io.Reader
		stdout io.Writer
		stderr io.Writer
		want   string
	}{
		{
			name:   "nil stdin",
			stdin:  nil,
			stdout: io.Discard,
			stderr: io.Discard,
			want:   "stdin is required",
		},
		{
			name:   "nil stdout",
			stdin:  strings.NewReader(""),
			stdout: nil,
			stderr: io.Discard,
			want:   "stdout is required",
		},
		{
			name:   "nil stderr",
			stdin:  strings.NewReader(""),
			stdout: io.Discard,
			stderr: nil,
			want:   "stderr is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := RunProxy(ctx, workingDir, Options{}, tc.stdin, tc.stdout, tc.stderr)
			if err == nil {
				t.Fatal("RunProxy() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunProxy() error = %q, want containing %q", err.Error(), tc.want)
			}
		})
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
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(fakeSession, "test-agent", codexToolConfig{}, &l)
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
	l, logBuf := newDebugTestLogger()
	agent := newCodexACPProxyAgent(fakeSession, "test-agent", codexToolConfig{}, l)
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
	assertLogContains(t, logBuf, `"message":"mcp tool call"`, `"method":"tools/call"`, `"phase":"request"`)
	assertLogContains(t, logBuf, `"message":"mcp tool call"`, `"method":"tools/call"`, `"phase":"response"`, `"status":"cancelled"`)
}

func TestCodexACPProxySessionFactoryCreatesDistinctBackendsPerSession(t *testing.T) {
	backends := make([]*fakeCodexMCPToolSession, 0, 2)
	factoryCalls := 0
	l := zerolog.Nop()
	agent := newCodexACPProxyAgentWithFactory(
		func(context.Context, string) (codexMCPToolSession, error) {
			factoryCalls++
			backend := &fakeCodexMCPToolSession{
				listTools: []*mcp.Tool{{Name: codexToolName}, {Name: codexReplyToolName}},
			}
			backends = append(backends, backend)
			return backend, nil
		},
		"test-agent",
		codexToolConfig{},
		&l,
	)
	agent.setConnection(&fakeACPSessionUpdater{})

	first, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work-1"})
	if err != nil {
		t.Fatalf("first NewSession() error = %v", err)
	}
	second, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work-2"})
	if err != nil {
		t.Fatalf("second NewSession() error = %v", err)
	}

	if first.SessionId == second.SessionId {
		t.Fatalf("session ids must differ, got %q", first.SessionId)
	}
	if factoryCalls != 2 {
		t.Fatalf("factory calls = %d, want 2", factoryCalls)
	}
	if len(backends) != 2 || backends[0] == backends[1] {
		t.Fatalf("expected distinct backend instances, got %#v", backends)
	}
}

func TestCodexACPProxySetModelResetsThreadAndBackend(t *testing.T) {
	backends := make([]*fakeCodexMCPToolSession, 0, 2)
	l, logBuf := newDebugTestLogger()
	agent := newCodexACPProxyAgentWithFactory(
		func(context.Context, string) (codexMCPToolSession, error) {
			backend := &fakeCodexMCPToolSession{
				listTools: []*mcp.Tool{{Name: codexToolName}, {Name: codexReplyToolName}},
				callResults: []*mcp.CallToolResult{
					{
						StructuredContent: map[string]any{
							"threadId": "thread-1",
							"content":  "response",
						},
					},
				},
			}
			backends = append(backends, backend)
			return backend, nil
		},
		"test-agent",
		codexToolConfig{},
		l,
	)
	agent.setConnection(&fakeACPSessionUpdater{})

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
	if len(backends) != 1 {
		t.Fatalf("backend count after first prompt = %d, want 1", len(backends))
	}
	firstCalls := backends[0].callsSnapshot()
	if len(firstCalls) == 0 || firstCalls[0].Name != codexToolName {
		t.Fatalf("first backend calls = %+v, want initial %q call", firstCalls, codexToolName)
	}

	if _, err := agent.SetSessionModel(context.Background(), acp.SetSessionModelRequest{
		SessionId: newResp.SessionId,
		ModelId:   "gpt-new",
	}); err != nil {
		t.Fatalf("SetSessionModel() error = %v", err)
	}
	if got := backends[0].closeCallsCount(); got == 0 {
		t.Fatalf("first backend close calls = %d, want > 0", got)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("second prompt")},
	}); err != nil {
		t.Fatalf("second Prompt() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("backend count after model change prompt = %d, want 2", len(backends))
	}
	secondCalls := backends[1].callsSnapshot()
	if len(secondCalls) == 0 || secondCalls[0].Name != codexToolName {
		t.Fatalf("second backend calls = %+v, want thread-reset %q call", secondCalls, codexToolName)
	}
	secondArgs, ok := secondCalls[0].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("second backend call args type = %T, want map[string]any", secondCalls[0].Arguments)
	}
	if _, exists := secondArgs["mode"]; exists {
		t.Fatalf("mode argument should not be propagated to codex tool call: args=%v", secondArgs)
	}
	assertLogContains(t, logBuf, `"message":"mcp tool call"`, `"method":"tools/call"`, `"phase":"request"`, `"tool_name":"codex"`)
	assertLogContains(t, logBuf, `"message":"mcp tool call"`, `"method":"tools/call"`, `"phase":"response"`, `"status":"completed"`)
	assertLogContains(t, logBuf, `"message":"starting mcp backend session"`, `"reason":"session_new"`)
	assertLogContains(t, logBuf, `"message":"mcp backend restart requested"`, `"reason":"session_set_model"`)
	assertLogContains(t, logBuf, `"message":"closing mcp backend for restart"`, `"reason":"session_set_model"`)
	assertLogContains(t, logBuf, `"message":"closed mcp backend for restart"`, `"reason":"session_set_model"`)
	assertLogContains(t, logBuf, `"message":"mcp backend session ready"`, `"reason":"session_set_model"`)
}

func TestCodexACPProxySetModelPreservesMCPServers(t *testing.T) {
	fakeSession := &fakeCodexMCPToolSession{
		listTools: []*mcp.Tool{{Name: codexToolName}, {Name: codexReplyToolName}},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(fakeSession, "test", codexToolConfig{}, &l)
	agent.setConnection(&fakeACPSessionUpdater{})

	mcpServers := []acp.McpServer{{
		Stdio: &acp.McpServerStdio{
			Name:    "preserved-server",
			Command: "echo",
		},
	}}

	// 1. Create session with mcpServers
	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		McpServers: mcpServers,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	// 2. Change model (should trigger backend reset)
	if _, err := agent.SetSessionModel(context.Background(), acp.SetSessionModelRequest{
		SessionId: resp.SessionId,
		ModelId:   "new-model",
	}); err != nil {
		t.Fatalf("SetSessionModel() error = %v", err)
	}

	// 3. Prompt (should use new backend, but include mcp_servers config because thread is reset)
	_, err = agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: resp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	calls := fakeSession.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}

	args := calls[0].Arguments.(map[string]any)
	mcpConfig, ok := mapArgMCPServers(args)
	if !ok {
		t.Fatalf("config.mcp_servers not found in args after SetSessionModel")
	}
	if len(mcpConfig) != 1 {
		t.Fatalf("mcp_servers len = %d, want 1", len(mcpConfig))
	}
	preserved, ok := mcpConfig["preserved-server"].(map[string]any)
	if !ok {
		t.Fatalf("preserved-server config missing or wrong type: %#v", mcpConfig["preserved-server"])
	}
	if got := mapArgString(preserved, "command"); got != "echo" {
		t.Errorf("command = %q, want %q", got, "echo")
	}
}

func TestCodexACPProxySetModeResetsThreadAndBackend(t *testing.T) {
	backends := make([]*fakeCodexMCPToolSession, 0, 2)
	l, logBuf := newDebugTestLogger()
	agent := newCodexACPProxyAgentWithFactory(
		func(context.Context, string) (codexMCPToolSession, error) {
			backend := &fakeCodexMCPToolSession{
				listTools: []*mcp.Tool{{Name: codexToolName}, {Name: codexReplyToolName}},
				callResults: []*mcp.CallToolResult{
					{
						StructuredContent: map[string]any{
							"threadId": "thread-1",
							"content":  "response",
						},
					},
				},
			}
			backends = append(backends, backend)
			return backend, nil
		},
		"test-agent",
		codexToolConfig{},
		l,
	)
	agent.setConnection(&fakeACPSessionUpdater{})

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

	if _, err := agent.SetSessionMode(context.Background(), acp.SetSessionModeRequest{
		SessionId: newResp.SessionId,
		ModeId:    "code",
	}); err != nil {
		t.Fatalf("SetSessionMode() error = %v", err)
	}
	if got := backends[0].closeCallsCount(); got == 0 {
		t.Fatalf("first backend close calls = %d, want > 0", got)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("second prompt")},
	}); err != nil {
		t.Fatalf("second Prompt() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("backend count after mode change prompt = %d, want 2", len(backends))
	}
	secondCalls := backends[1].callsSnapshot()
	if len(secondCalls) == 0 || secondCalls[0].Name != codexToolName {
		t.Fatalf("second backend calls = %+v, want thread-reset %q call", secondCalls, codexToolName)
	}
	assertLogContains(t, logBuf, `"message":"mcp backend restart requested"`, `"reason":"session_set_mode"`)
	assertLogContains(t, logBuf, `"message":"closing mcp backend for restart"`, `"reason":"session_set_mode"`)
	assertLogContains(t, logBuf, `"message":"closed mcp backend for restart"`, `"reason":"session_set_mode"`)
	assertLogContains(t, logBuf, `"message":"mcp backend session ready"`, `"reason":"session_set_mode"`)
}

func TestCodexACPProxyInitializeUsesConfiguredAgentName(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(&fakeCodexMCPToolSession{}, "team-codex", codexToolConfig{}, &l)
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
	if resp.AgentInfo.Version != DefaultAgentVersion {
		t.Fatalf("AgentInfo.Version = %q, want %q", resp.AgentInfo.Version, DefaultAgentVersion)
	}
}

func TestCodexACPProxyInitializeUsesDefaultAgentNameWhenEmpty(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(&fakeCodexMCPToolSession{}, "", codexToolConfig{}, &l)
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
	if resp.AgentInfo.Version != DefaultAgentVersion {
		t.Fatalf("AgentInfo.Version = %q, want %q", resp.AgentInfo.Version, DefaultAgentVersion)
	}
}

func TestCodexACPProxyInitializeUsesConfiguredAgentVersion(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(&fakeCodexMCPToolSession{}, "team-codex", codexToolConfig{}, &l)
	agent.setAgentVersion(testMCPVersion)

	resp, err := agent.Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentInfo == nil {
		t.Fatalf("AgentInfo is nil")
	}
	if resp.AgentInfo.Version != testMCPVersion {
		t.Fatalf("AgentInfo.Version = %q, want %q", resp.AgentInfo.Version, testMCPVersion)
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

	var logs bytes.Buffer
	logger := zerolog.New(&logs).Level(zerolog.DebugLevel)
	var stderr bytes.Buffer
	ctx := logger.WithContext(context.Background())
	runErr := RunProxy(
		ctx,
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
	if strings.Contains(stderr.String(), "peer connection closed") {
		t.Fatalf("stderr contains unexpected ACP connection diagnostics: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "failed to close session backend") {
		t.Fatalf("stderr contains unexpected backend-close warning: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "failed waiting for session backend stop") {
		t.Fatalf("stderr contains unexpected backend-stop warning: %q", stderr.String())
	}
	if !strings.Contains(logs.String(), `"message":"starting codex acp bridge"`) {
		t.Fatalf("logs do not contain bridge startup message: %q", logs.String())
	}
	if !strings.Contains(logs.String(), `"cmd":"codex"`) {
		t.Fatalf("logs do not contain command name: %q", logs.String())
	}
	if !strings.Contains(logs.String(), `"args":["mcp-server"]`) {
		t.Fatalf("logs do not contain command args: %q", logs.String())
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

func newDebugTestLogger() (*zerolog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := zerolog.New(buf).Level(zerolog.DebugLevel)
	return &logger, buf
}

func assertLogContains(t *testing.T, buf *bytes.Buffer, wants ...string) {
	t.Helper()
	logs := buf.String()
	for _, want := range wants {
		if !strings.Contains(logs, want) {
			t.Fatalf("log output does not contain %q\nlogs=%s", want, logs)
		}
	}
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

func mapArgMCPServers(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	cfg, ok := m["config"].(map[string]any)
	if !ok {
		return nil, false
	}
	servers, ok := cfg["mcp_servers"].(map[string]any)
	if !ok {
		return nil, false
	}
	return servers, true
}

type fakeCodexToolCall struct {
	Name      string
	Arguments any
}

type fakeCodexMCPToolSession struct {
	mu sync.Mutex

	listTools        []*mcp.Tool
	initializeResult *mcp.InitializeResult
	callResults      []*mcp.CallToolResult
	callHook         func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	closeErr         error
	waitErr          error
	calls            []fakeCodexToolCall
	closeCalls       int
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

func (s *fakeCodexMCPToolSession) InitializeResult() *mcp.InitializeResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initializeResult == nil {
		return nil
	}
	result := *s.initializeResult
	return &result
}

func (s *fakeCodexMCPToolSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
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

func (s *fakeCodexMCPToolSession) closeCallsCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
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

func TestNewSession_WithMCP_Stdio(t *testing.T) {
	testNewSessionWithMCPTransport(t, acp.McpServer{
		Stdio: &acp.McpServerStdio{
			Name:    "my-stdio",
			Command: "echo",
		},
	}, "my-stdio", mcpTransportStdio)
}

func TestNewSession_WithMCP_HTTP(t *testing.T) {
	testNewSessionWithMCPTransport(t, acp.McpServer{
		Http: &acp.McpServerHttp{
			Name: "my-http",
			Url:  "http://localhost",
		},
	}, "my-http", mcpTransportHTTP)
}

func testNewSessionWithMCPTransport(t *testing.T, server acp.McpServer, wantName, wantTransport string) {
	t.Helper()

	fakeSession := &fakeCodexMCPToolSession{
		listTools: []*mcp.Tool{{Name: codexToolName}, {Name: codexReplyToolName}},
	}
	l, logBuf := newDebugTestLogger()
	agent := newCodexACPProxyAgent(fakeSession, "test", codexToolConfig{}, l)
	agent.setConnection(&fakeACPSessionUpdater{})

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		McpServers: []acp.McpServer{server},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	_, err = agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: resp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	calls := fakeSession.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}

	args := calls[0].Arguments.(map[string]any)
	mcpConfig, ok := mapArgMCPServers(args)
	if !ok {
		t.Fatalf("config.mcp_servers not found in args")
	}
	if len(mcpConfig) != 1 {
		t.Fatalf("mcp_servers len = %d, want 1", len(mcpConfig))
	}
	serverCfg, ok := mcpConfig[wantName].(map[string]any)
	if !ok {
		t.Fatalf("server %q missing or wrong type: %#v", wantName, mcpConfig[wantName])
	}
	switch wantTransport {
	case mcpTransportStdio:
		if _, ok := serverCfg["command"]; !ok {
			t.Errorf("stdio server missing command: %#v", serverCfg)
		}
	case mcpTransportHTTP:
		if got := mapArgString(serverCfg, "url"); got == "" {
			t.Errorf("http server missing url: %#v", serverCfg)
		}
	default:
		t.Fatalf("unsupported transport %q in test", wantTransport)
	}
	assertLogContains(t, logBuf, `"message":"starting mcp backend session"`, `"reason":"session_new"`)
	assertLogContains(t, logBuf, `"message":"mcp backend session ready"`, `"reason":"session_new"`, `"mcp_server_count":1`)
}

func TestNewSession_WithMCP_RejectInvalid(t *testing.T) {
	fakeSession := &fakeCodexMCPToolSession{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(fakeSession, "test", codexToolConfig{}, &l)

	// No transport
	_, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		McpServers: []acp.McpServer{{}},
	})
	if err == nil {
		t.Fatal("expected error for empty server config")
	}

	// SSE (not supported)
	_, err = agent.NewSession(context.Background(), acp.NewSessionRequest{
		McpServers: []acp.McpServer{{
			Sse: &acp.McpServerSse{Name: "sse", Url: "http://sse"},
		}},
	})
	if err == nil {
		t.Fatal("expected error for sse transport")
	}

	// Multiple transports on one server (ambiguous)
	_, err = agent.NewSession(context.Background(), acp.NewSessionRequest{
		McpServers: []acp.McpServer{{
			Stdio: &acp.McpServerStdio{Name: "mixed", Command: "echo"},
			Http:  &acp.McpServerHttp{Name: "mixed", Url: "http://localhost"},
		}},
	})
	if err == nil {
		t.Fatal("expected error for multiple transports in one mcp server entry")
	}
}
