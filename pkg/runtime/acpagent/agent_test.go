package acpagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	runnerpkg "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	testACPCallID = "call-1"
	testACPToolID = "tool-1"
)

func TestClientPromptReceivesUpdates(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)

	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates, resultCh, err := client.Prompt(context.Background(), string(sess.SessionId), "hello")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	var chunks []string
	for note := range updates {
		ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", ExtendedSessionNotification{SessionNotification: note.SessionNotification, Raw: note.Raw})
		if ok {
			if text := extractPromptText(ev.Content); text != "" {
				chunks = append(chunks, text)
			}
		}
	}
	result := <-resultCh
	if result.Err != nil {
		t.Fatalf("PromptResult.Err = %v", result.Err)
	}
	if result.Response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", result.Response.StopReason, acp.StopReasonEndTurn)
	}
	got := strings.Join(chunks, "")
	want := string(sess.SessionId) + ":hello"
	if got != want {
		t.Fatalf("joined chunks = %q, want %q", got, want)
	}
}

func TestClientCreateSessionSetsModel(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_SESSION_MODEL": "openai/gpt-5.4",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.CreateSession(context.Background(), t.TempDir(), "openai/gpt-5.4", "", nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("CreateSession() returned empty session id")
	}
}

func TestClientCreateSessionIgnoresSetModelUnsupported(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_DISABLE_SET_MODEL": "1",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.CreateSession(context.Background(), t.TempDir(), "openai/gpt-5.4", "", nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("CreateSession() returned empty session id")
	}
}

func TestClientCreateSessionFailsOnSetModelRequestError(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_SESSION_MODEL": "different/model",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	_, err = client.CreateSession(context.Background(), t.TempDir(), "openai/gpt-5.4", "", nil)
	if err == nil {
		t.Fatal("CreateSession() error = nil, want set model error")
	}
}

func TestClientCreateSessionSetsMode(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_SESSION_MODE": "code",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.CreateSession(context.Background(), t.TempDir(), "", "code", nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("CreateSession() returned empty session id")
	}
}

func TestClientCreateSessionIgnoresSetModeUnsupported(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_DISABLE_SET_MODE": "1",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.CreateSession(context.Background(), t.TempDir(), "", "code", nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("CreateSession() returned empty session id")
	}
}

func TestClientCreateSessionFailsOnSetModeRequestError(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_SESSION_MODE": "different-mode",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	_, err = client.CreateSession(context.Background(), t.TempDir(), "", "code", nil)
	if err == nil {
		t.Fatal("CreateSession() error = nil, want set mode error")
	}
}

func TestClientSuppressesPeerDisconnectInfoByDefault(t *testing.T) {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prev)
	})

	var stderr bytes.Buffer
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.NewSession(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	_ = client.Close()
	if got := stderr.String(); strings.Contains(got, "peer connection closed") {
		t.Fatalf("stderr contains peer disconnect noise: %q", got)
	}
}

func TestClientLogsPeerDisconnectInfoInDebug(t *testing.T) {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(prev)
	})

	logger := newACPConnectionLogger(io.Discard)
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("connection logger should enable info level when global level is debug")
	}
}

func TestWireLogBufferSuppressesWirePayloadInDebug(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	buf := newWireLogBuffer("send", logger, nil)
	buf.logLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`))

	if got := logBuf.String(); strings.Contains(got, "acp wire") {
		t.Fatalf("debug log unexpectedly contains trace-only wire payload: %q", got)
	}
}

func TestWireLogBufferEmitsWirePayloadInTrace(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	buf := newWireLogBuffer("recv", logger, nil)
	buf.logLine([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))

	got := logBuf.String()
	if !strings.Contains(got, "acp wire") {
		t.Fatalf("trace log missing wire payload marker: %q", got)
	}
	if !strings.Contains(got, `"direction":"recv"`) {
		t.Fatalf("trace log missing direction field: %q", got)
	}
}

func TestClientCloseSuppressesExpectedProcessExitWarnings(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
		Stderr:  io.Discard,
		Logger:  &logger,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.NewSession(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got := logBuf.String()
	if strings.Contains(got, "acp process exited with error") {
		t.Fatalf("log unexpectedly contains process exit warning: %q", got)
	}
	if strings.Contains(got, "failed to kill acp process") {
		t.Fatalf("log unexpectedly contains kill warning: %q", got)
	}
	if strings.Contains(got, "failed to close stdin") {
		t.Fatalf("log unexpectedly contains stdin close warning: %q", got)
	}
}

func TestClientHandlesPermissionRequest(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
		PermissionHandler: func(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(req.Options[0].OptionId)}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)

	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates, resultCh, err := client.Prompt(context.Background(), string(sess.SessionId), "permission")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	var chunks []string
	for note := range updates {
		ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", ExtendedSessionNotification{SessionNotification: note.SessionNotification, Raw: note.Raw})
		if ok {
			if text := extractPromptText(ev.Content); text != "" {
				chunks = append(chunks, text)
			}
		}
	}
	result := <-resultCh
	if result.Err != nil {
		t.Fatalf("PromptResult.Err = %v", result.Err)
	}
	if got := strings.Join(chunks, ""); got != "approved" {
		t.Fatalf("joined chunks = %q, want approved", got)
	}
}

func TestClientInitializeUsesDefaultIdentity(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_CLIENT_NAME":    "norma-acpagent",
			"GO_EXPECT_CLIENT_VERSION": "dev",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
}

func TestClientInitializeUsesConfiguredIdentity(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_CLIENT_NAME":    "custom-acp-client",
			"GO_EXPECT_CLIENT_VERSION": "1.2.3",
		}),
		ClientName:    "custom-acp-client",
		ClientVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
}

func TestClientPromptAllowsConcurrentDifferentSessions(t *testing.T) {
	const (
		wantSession1 = "session-1:one"
		wantSession2 = "session-2:two"
	)

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess1, err := client.NewSession(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	sess2, err := client.NewSession(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates1, resultCh1, err := client.Prompt(context.Background(), string(sess1.SessionId), "slow:one")
	if err != nil {
		t.Fatalf("Prompt(session1) error = %v", err)
	}
	updates2, resultCh2, err := client.Prompt(context.Background(), string(sess2.SessionId), "two")
	if err != nil {
		t.Fatalf("Prompt(session2) error = %v", err)
	}

	got1 := readPromptOutput(t, updates1, resultCh1)
	got2 := readPromptOutput(t, updates2, resultCh2)
	if got1 != wantSession1 {
		t.Fatalf("session1 output = %q, want %q", got1, wantSession1)
	}
	if got2 != wantSession2 {
		t.Fatalf("session2 output = %q, want %q", got2, wantSession2)
	}
}

func TestClientPromptRejectsConcurrentSameSession(t *testing.T) {
	const wantSession1 = "session-1:one"

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)

	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates1, resultCh1, err := client.Prompt(context.Background(), string(sess.SessionId), "slow:one")
	if err != nil {
		t.Fatalf("Prompt(first) error = %v", err)
	}
	if _, _, err := client.Prompt(context.Background(), string(sess.SessionId), "two"); !errors.Is(err, ErrPromptAlreadyActive) {
		t.Fatalf("Prompt(second) error = %v, want %v", err, ErrPromptAlreadyActive)
	}

	got1 := readPromptOutput(t, updates1, resultCh1)
	if got1 != wantSession1 {
		t.Fatalf("session output = %q, want %q", got1, wantSession1)
	}
}

func TestClientPromptValidatesInputs(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommand(t),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	testCases := []struct {
		name      string
		sessionID string
		prompt    string
		wantErr   error
	}{
		{
			name:      "missing session id",
			sessionID: " ",
			prompt:    "prompt",
			wantErr:   errSessionIDRequired,
		},
		{
			name:      "missing prompt",
			sessionID: "session-1",
			prompt:    " ",
			wantErr:   errPromptRequired,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, gotErr := client.Prompt(context.Background(), tc.sessionID, tc.prompt)
			if !errors.Is(gotErr, tc.wantErr) {
				t.Fatalf("Prompt() error = %v, want %v", gotErr, tc.wantErr)
			}
		})
	}
}

func TestClientSessionUpdateCallbackLogsContentBlock(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	client := &Client{logger: logger}
	err := client.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "session-1",
		Update: acp.SessionUpdate{
			UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
				Content: acp.ContentBlock{},
			},
		},
	})
	if err != nil {
		t.Fatalf("SessionUpdate() error = %v", err)
	}

	got := logBuf.String()
	if !strings.Contains(got, "received acp session update callback") {
		t.Fatalf("debug log = %q, want callback message", got)
	}
	if !strings.Contains(got, "\"acp_content_block\":{\"type\":\"unknown\"}") {
		t.Fatalf("debug log = %q, want structured content block payload", got)
	}
	if !strings.Contains(got, "\"update_kind\":\"user_message_chunk\"") {
		t.Fatalf("debug log = %q, want update kind", got)
	}
	if !strings.Contains(got, "\"partial\":true") {
		t.Fatalf("debug log = %q, want partial flag", got)
	}
	if !strings.Contains(got, "\"thought\":false") {
		t.Fatalf("debug log = %q, want thought flag", got)
	}
}

func TestClientLogsSessionUpdateAtTraceOnly(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	client := &Client{
		logger: logger,
	}

	ext := ExtendedSessionNotification{
		SessionNotification: acp.SessionNotification{
			SessionId: "session-1",
			Update: acp.SessionUpdate{
				AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
					Content: acp.ContentBlock{},
				},
			},
		},
	}
	client.dispatchSessionUpdate(ext)

	got := logBuf.String()
	if strings.Contains(got, "received acp session update") {
		t.Fatalf("debug log unexpectedly contains trace-only session update: %q", got)
	}
}

func TestClientLogsSessionUpdateAtTrace(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)

	client := &Client{
		logger: logger,
	}

	ext := ExtendedSessionNotification{
		SessionNotification: acp.SessionNotification{
			SessionId: "session-1",
			Update: acp.SessionUpdate{
				AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
					Content: acp.ContentBlock{},
				},
			},
		},
	}
	client.dispatchSessionUpdate(ext)

	got := logBuf.String()
	if !strings.Contains(got, "received acp session update") {
		t.Fatalf("trace log missing session update message: %q", got)
	}
}

func TestClientLogsLastChunkInSeries(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	client := &Client{
		logger: logger,
		activeBySession: map[acp.SessionId]*activePrompt{
			"session-1": {
				sessionID: "session-1",
				logger:    logger,
				lastChunk: &loggedACPChunk{
					kind:         "agent_thought_chunk",
					contentBlock: map[string]any{"type": "unknown"},
					partial:      true,
					thought:      true,
				},
			},
		},
	}

	client.logLastChunkInSeries("session-1")

	got := logBuf.String()
	if !strings.Contains(got, "received last acp chunk in series") {
		t.Fatalf("debug log = %q, want last chunk message", got)
	}
	if !strings.Contains(got, "\"last_in_series\":true") {
		t.Fatalf("debug log = %q, want last_in_series flag", got)
	}
	if !strings.Contains(got, "\"thought\":true") {
		t.Fatalf("debug log = %q, want thought flag", got)
	}
	if !strings.Contains(got, "\"update_kind\":\"agent_thought_chunk\"") {
		t.Fatalf("debug log = %q, want update kind", got)
	}
}

func TestRequestPermissionPassesContextToHandler(t *testing.T) {
	type key string
	const ctxKey key = "ctx-key"
	const ctxVal = "ctx-value"

	var seen string
	c := &Client{
		logger: zerolog.Nop(),
		permissionHandler: func(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			seen, _ = ctx.Value(ctxKey).(string)
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		},
	}

	_, err := c.RequestPermission(context.WithValue(context.Background(), ctxKey, ctxVal), acp.RequestPermissionRequest{
		SessionId: "session-1",
		Options:   []acp.PermissionOption{},
	})
	if err != nil {
		t.Fatalf("RequestPermission() error = %v", err)
	}
	if seen != ctxVal {
		t.Fatalf("permission handler context value = %q, want %q", seen, ctxVal)
	}
}

func TestAgentReusesRemoteSession(t *testing.T) {
	a, err := New(Config{
		Context:    context.Background(),
		Command:    helperCommand(t),
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	first := collectFinalText(t, r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("one", genai.RoleUser), agent.RunConfig{}))
	second := collectFinalText(t, r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("two", genai.RoleUser), agent.RunConfig{}))

	if first != "session-1:one" {
		t.Fatalf("first final text = %q, want session-1:one", first)
	}
	if second != "session-1:two" {
		t.Fatalf("second final text = %q, want session-1:two", second)
	}
}

func TestAgentUsesReturnedACPSessionIDWhenConfiguredSessionIDDiffers(t *testing.T) {
	const sessionID = "tg-2317500-0"
	const acpSessionID = "session-1"

	a, err := New(Config{
		Context: context.Background(),
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_NEW_SESSION_META_SESSION_ID": sessionID,
			"GO_FORCE_NEW_SESSION_ID":               acpSessionID,
		}),
		WorkingDir: t.TempDir(),
		SessionID:  sessionID,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got := collectFinalText(t, r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("hello", genai.RoleUser), agent.RunConfig{}))
	want := acpSessionID + ":hello"
	if got != want {
		t.Fatalf("final text = %q, want %q", got, want)
	}
}

func TestAgentFailsFastOnMissingRemoteSessionDuringPrompt(t *testing.T) {
	a, err := New(Config{
		Context: context.Background(),
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_FAIL_FIRST_PROMPT_ENTITY_NOT_FOUND": "1",
		}),
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var firstRunErr error
	for _, runErr := range r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("hello", genai.RoleUser), agent.RunConfig{}) {
		if runErr != nil {
			firstRunErr = runErr
		}
	}
	if firstRunErr == nil {
		t.Fatal("first run error = nil, want prompt error")
	}
	if got := firstRunErr.Error(); !strings.Contains(got, "Requested entity was not found") {
		t.Fatalf("first run error = %q, want missing-entity error", got)
	}

	second := collectFinalText(t, r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("again", genai.RoleUser), agent.RunConfig{}))
	if second != "session-1:again" {
		t.Fatalf("second final text = %q, want session-1:again", second)
	}
}

func collectFinalText(t *testing.T, events iter.Seq2[*session.Event, error]) string {
	t.Helper()
	var fullText strings.Builder
	turnCompleteSeen := false
	for ev, err := range events {
		if err != nil {
			t.Fatalf("runner event error = %v", err)
		}
		if ev == nil {
			continue
		}
		if ev.TurnComplete {
			turnCompleteSeen = true
		}
		text := extractPromptText(ev.Content)
		fullText.WriteString(text)
	}
	if !turnCompleteSeen {
		t.Fatalf("expected turn complete event")
	}
	return fullText.String()
}

func TestAgentRunDoesNotDuplicatePartialInFinalEvent(t *testing.T) {
	a, err := New(Config{
		Context:    context.Background(),
		Command:    helperCommand(t),
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var accumulatedText strings.Builder
	turnCompleteSeen := false
	for ev, err := range r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("hello", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("runner event error = %v", err)
		}
		if ev == nil {
			continue
		}
		if ev.TurnComplete {
			turnCompleteSeen = true
		}
		text := extractPromptText(ev.Content)
		accumulatedText.WriteString(text)
	}
	if !turnCompleteSeen {
		t.Fatalf("expected turn complete event")
	}
	got := accumulatedText.String()
	if got != "session-1:hello" {
		t.Fatalf("accumulated text = %q, want %q", got, "session-1:hello")
	}
}

func TestAgentRunUsesInvocationLogger(t *testing.T) {
	var bootstrapBuf bytes.Buffer
	bootstrapLogger := zerolog.New(zerolog.SyncWriter(&bootstrapBuf)).Level(zerolog.DebugLevel)

	a, err := New(Config{
		Context:    context.Background(),
		Command:    helperCommand(t),
		WorkingDir: t.TempDir(),
		Logger:     &bootstrapLogger,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = a.Close()
		}
	}()
	bootstrapBuf.Reset()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var invocationBuf bytes.Buffer
	invocationLogger := zerolog.New(zerolog.SyncWriter(&invocationBuf)).Level(zerolog.DebugLevel).With().Str("source", "invocation").Logger()
	invocationCtx := invocationLogger.WithContext(context.Background())

	for _, runErr := range r.Run(invocationCtx, "test-user", sess.Session.ID(), genai.NewContentFromText("hello", genai.RoleUser), agent.RunConfig{}) {
		if runErr != nil {
			t.Fatalf("runner event error = %v", runErr)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	closed = true

	invocationLogs := invocationBuf.String()
	if !strings.Contains(invocationLogs, `"source":"invocation"`) {
		t.Fatalf("invocation log missing source marker: %q", invocationLogs)
	}
	for _, mustContain := range []string{"starting adk invocation", "sending acp session/prompt"} {
		if !strings.Contains(invocationLogs, mustContain) {
			t.Fatalf("invocation log missing %q: %q", mustContain, invocationLogs)
		}
	}

	if got := bootstrapBuf.String(); strings.Contains(got, "starting adk invocation") || strings.Contains(got, "sending acp session/prompt") {
		t.Fatalf("bootstrap logger unexpectedly received invocation logs: %q", got)
	}
}

func TestAgentRunMapsACPEventsToADKEvents(t *testing.T) {
	a, err := New(Config{
		Context:    context.Background(),
		Command:    helperCommand(t),
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	seenCall := false
	seenUpdate := false
	seenMessage := false
	seenTurnComplete := false

	for ev, err := range r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("tooling", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("runner event error = %v", err)
		}
		if ev == nil {
			continue
		}
		if ev.TurnComplete {
			seenTurnComplete = true
		}
		if ev.Content == nil {
			continue
		}
		if ev.Partial && extractPromptText(ev.Content) == "tooling-done" {
			seenMessage = true
		}
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil && part.FunctionCall.ID == testACPToolID && part.FunctionCall.Name == "acp_tool_call" {
				seenCall = true
			}
			if part.FunctionResponse != nil && part.FunctionResponse.ID == testACPToolID && part.FunctionResponse.Name == "acp_tool_call_update" {
				seenUpdate = true
			}
		}
	}

	if !seenCall {
		t.Fatalf("expected mapped tool call event")
	}
	if !seenUpdate {
		t.Fatalf("expected mapped tool call update event")
	}
	if !seenMessage {
		t.Fatalf("expected mapped agent message chunk event")
	}
	if !seenTurnComplete {
		t.Fatalf("expected final turn complete event")
	}
}

func TestClientCreateSessionSetsMCPServers(t *testing.T) {
	expectedServers := []acp.McpServer{
		{
			Stdio: &acp.McpServerStdio{
				Name:    "test-server",
				Command: "echo",
				Args:    []string{"hello"},
			},
		},
	}
	expectedJSON, _ := json.Marshal(expectedServers)

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_MCP_SERVERS": string(expectedJSON),
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.NewSession(context.Background(), t.TempDir(), expectedServers)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("NewSession() returned empty session id")
	}
}

func TestClientNewSessionSendsEmptyMCPServersArrayWhenNil(t *testing.T) {
	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_MCP_SERVERS_RAW": "[]",
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("NewSession() returned empty session id")
	}
}

func TestClientNewSession_UsesConfiguredSessionIDAndMeta(t *testing.T) {
	const sessionID = "tg-2317500-0"

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_NEW_SESSION_META_SESSION_ID": sessionID,
			"GO_FORCE_NEW_SESSION_ID":               sessionID,
		}),
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got := string(sess.SessionId); got != sessionID {
		t.Fatalf("session id = %q, want %q", got, sessionID)
	}
}

func TestClientNewSession_AcceptsDifferentSessionIDFromACP(t *testing.T) {
	const sessionID = "tg-2317500-0"
	const acpSessionID = "session-1"

	client, err := NewClient(context.Background(), ClientConfig{
		Command: helperCommandWithEnv(t, map[string]string{
			"GO_EXPECT_NEW_SESSION_META_SESSION_ID": sessionID,
			"GO_FORCE_NEW_SESSION_ID":               acpSessionID,
		}),
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sess, err := client.NewSession(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got := string(sess.SessionId); got != acpSessionID {
		t.Fatalf("session id = %q, want %q", got, acpSessionID)
	}
}

func TestAgentConfigMCPServersUseEmptyArraysNotNull(t *testing.T) {
	expectedRaw := `[{"headers":[],"name":"http_server","type":"http","url":"http://localhost:9999/mcp"},{"headers":[],"name":"sse_server","type":"sse","url":"http://localhost:9998/sse"},{"args":[],"command":"echo","env":[],"name":"stdio_server"}]`

	a, err := New(Config{
		Context:    context.Background(),
		Command:    helperCommandWithEnv(t, map[string]string{"GO_EXPECT_MCP_SERVERS_RAW": expectedRaw}),
		WorkingDir: t.TempDir(),
		MCPServers: map[string]MCPServerConfig{
			"stdio_server": {
				Type: MCPServerTypeStdio,
				Cmd:  []string{"echo"},
			},
			"http_server": {
				Type: MCPServerTypeHTTP,
				URL:  "http://localhost:9999/mcp",
			},
			"sse_server": {
				Type: MCPServerTypeSSE,
				URL:  "http://localhost:9998/sse",
			},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{AppName: "test-app", UserID: "test-user"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	for _, runErr := range r.Run(context.Background(), "test-user", sess.Session.ID(), genai.NewContentFromText("ping", genai.RoleUser), agent.RunConfig{}) {
		if runErr != nil {
			t.Fatalf("runner event error = %v", runErr)
		}
	}
}

func helperCommand(t *testing.T) []string {
	return helperCommandWithEnv(t, nil)
}

func helperCommandWithEnv(t *testing.T, env map[string]string) []string {
	t.Helper()
	cmd := []string{"env", "GO_WANT_ACP_HELPER=1"}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			cmd = append(cmd, key+"="+env[key])
		}
	}
	cmd = append(cmd, os.Args[0], "-test.run=TestACPHelperProcess", "--")
	return cmd
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	runACPHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func runACPHelper(stdin *os.File, stdout *os.File) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	sessionCount := 0
	promptCount := 0
	expectedClientName := os.Getenv("GO_EXPECT_CLIENT_NAME")
	expectedClientVersion := os.Getenv("GO_EXPECT_CLIENT_VERSION")
	expectedSessionModel := os.Getenv("GO_EXPECT_SESSION_MODEL")
	expectedSessionMode := os.Getenv("GO_EXPECT_SESSION_MODE")
	expectedMCPServers := os.Getenv("GO_EXPECT_MCP_SERVERS")
	expectedMCPServersRaw := os.Getenv("GO_EXPECT_MCP_SERVERS_RAW")
	expectedNewSessionMetaSessionID := os.Getenv("GO_EXPECT_NEW_SESSION_META_SESSION_ID")
	forceNewSessionID := os.Getenv("GO_FORCE_NEW_SESSION_ID")
	disableSetModel := os.Getenv("GO_DISABLE_SET_MODEL") == "1"
	disableSetMode := os.Getenv("GO_DISABLE_SET_MODE") == "1"
	failFirstPromptEntityNotFound := os.Getenv("GO_FAIL_FIRST_PROMPT_ENTITY_NOT_FOUND") == "1"

	for scanner.Scan() {
		var msg helperEnvelope
		must(json.Unmarshal(scanner.Bytes(), &msg))
		switch msg.Method {
		case acp.AgentMethodInitialize:
			var req helperInitializeRequest
			must(json.Unmarshal(msg.Params, &req))
			if expectedClientName != "" && req.ClientInfo.Name != expectedClientName {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected client name: %s", req.ClientInfo.Name)},
				})
				continue
			}
			if expectedClientVersion != "" && req.ClientInfo.Version != expectedClientVersion {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected client version: %s", req.ClientInfo.Version)},
				})
				continue
			}
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperInitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber})})
		case acp.AgentMethodSessionNew:
			var req helperNewSessionRequest
			must(json.Unmarshal(msg.Params, &req))
			if expectedMCPServersRaw != "" {
				var reqRaw struct {
					McpServers json.RawMessage `json:"mcpServers"`
				}
				must(json.Unmarshal(msg.Params, &reqRaw))
				gotRaw := compactJSONForCompare(reqRaw.McpServers)
				wantRaw := compactJSONForCompare([]byte(expectedMCPServersRaw))
				if gotRaw != wantRaw {
					writeEnvelope(stdout, helperEnvelope{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected raw mcp servers payload: %q, want %q", gotRaw, wantRaw)},
					})
					continue
				}
			}
			if expectedMCPServers != "" {
				gotJSON, _ := json.Marshal(req.McpServers)
				// Basic string comparison of JSON might be flaky if key order differs,
				// but for simple struct it might work if mostly empty.
				// Better to unmarshal expected and compare.
				var expected []acp.McpServer
				must(json.Unmarshal([]byte(expectedMCPServers), &expected))

				// Re-marshal both to ensure consistent ordering/formatting if possible,
				// or just check count and first element name.
				if len(req.McpServers) != len(expected) {
					writeEnvelope(stdout, helperEnvelope{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected mcp servers count: %d, want %d", len(req.McpServers), len(expected))},
					})
					continue
				}
				if len(expected) > 0 {
					if req.McpServers[0].Stdio == nil || expected[0].Stdio == nil || req.McpServers[0].Stdio.Name != expected[0].Stdio.Name {
						writeEnvelope(stdout, helperEnvelope{
							JSONRPC: "2.0",
							ID:      msg.ID,
							Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected mcp server: %s", string(gotJSON))},
						})
						continue
					}
				}
			}
			if expectedNewSessionMetaSessionID != "" {
				metaSessionID := ""
				if req.Meta != nil {
					if raw, ok := req.Meta["sessionId"]; ok {
						metaSessionID, _ = raw.(string)
					}
				}
				if metaSessionID != expectedNewSessionMetaSessionID {
					writeEnvelope(stdout, helperEnvelope{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected session/new _meta.sessionId: %q, want %q", metaSessionID, expectedNewSessionMetaSessionID)},
					})
					continue
				}
			}

			sessionCount++
			sessionID := fmt.Sprintf("session-%d", sessionCount)
			if forceNewSessionID != "" {
				sessionID = forceNewSessionID
			}
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperNewSessionResponse{SessionID: sessionID})})
		case acp.AgentMethodSessionSetModel:
			if disableSetModel {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32601, Message: "unsupported"},
				})
				continue
			}
			var req helperSetSessionModelRequest
			must(json.Unmarshal(msg.Params, &req))
			if expectedSessionModel != "" && req.ModelID != expectedSessionModel {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected session model: %s", req.ModelID)},
				})
				continue
			}
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperSetSessionModelResponse{})})
		case acp.AgentMethodSessionSetMode:
			if disableSetMode {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32601, Message: "unsupported"},
				})
				continue
			}
			var req helperSetSessionModeRequest
			must(json.Unmarshal(msg.Params, &req))
			if expectedSessionMode != "" && req.ModeID != expectedSessionMode {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error:   &helperError{Code: -32000, Message: fmt.Sprintf("unexpected session mode: %s", req.ModeID)},
				})
				continue
			}
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperSetSessionModeResponse{})})
		case acp.AgentMethodSessionPrompt:
			var req helperPromptRequest
			must(json.Unmarshal(msg.Params, &req))
			promptCount++
			if failFirstPromptEntityNotFound && promptCount == 1 {
				writeEnvelope(stdout, helperEnvelope{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Error: &helperError{
						Code:    500,
						Message: "Requested entity was not found.",
					},
				})
				continue
			}
			prompt := req.Prompt[0].Text
			if strings.HasPrefix(prompt, "slow:") {
				time.Sleep(150 * time.Millisecond)
				prompt = strings.TrimPrefix(prompt, "slow:")
			}
			if prompt == "permission" {
				title := "Edit file"
				writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: json.RawMessage(`"perm-1"`), Method: acp.ClientMethodSessionRequestPermission, Params: mustJSON(helperPermissionRequest{
					SessionID: req.SessionID,
					ToolCall:  helperPermissionToolCall{Title: &title},
					Options: []helperPermissionOption{
						{Kind: string(acp.PermissionOptionKindAllowOnce), Name: "Allow", OptionID: "allow"},
						{Kind: string(acp.PermissionOptionKindRejectOnce), Name: "Reject", OptionID: "reject"},
					},
				})})
				if !scanner.Scan() {
					return
				}
				var permitResp helperEnvelope
				must(json.Unmarshal(scanner.Bytes(), &permitResp))
				var outcome helperPermissionResponse
				must(json.Unmarshal(permitResp.Result, &outcome))
				text := "rejected"
				if outcome.Outcome.Outcome == "selected" && outcome.Outcome.OptionID == "allow" {
					text = "approved"
				}
				writeUpdate(stdout, req.SessionID, text)
				writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperPromptResponse{StopReason: string(acp.StopReasonEndTurn)})})
				continue
			}
			if prompt == "tooling" {
				writeToolCall(stdout, req.SessionID, testACPToolID, "run shell", acp.ToolCallStatusInProgress)
				writeToolCallUpdate(stdout, req.SessionID, testACPToolID, acp.ToolCallStatusCompleted, map[string]any{"ok": true})
				writeUpdate(stdout, req.SessionID, "tooling-done")
				writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperPromptResponse{StopReason: string(acp.StopReasonEndTurn)})})
				continue
			}
			prefix := req.SessionID + ":"
			writeUpdate(stdout, req.SessionID, prefix)
			writeUpdate(stdout, req.SessionID, prompt)
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperPromptResponse{StopReason: string(acp.StopReasonEndTurn)})})
		case acp.AgentMethodSessionCancel:
			// Ignore in helper.
		default:
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Error: &helperError{Code: -32601, Message: "unsupported"}})
		}
	}
}

func writeUpdate(stdout *os.File, sessionID, text string) {
	writeSessionUpdate(stdout, sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": text,
		},
	})
}

func writeToolCall(stdout *os.File, sessionID, toolCallID, title string, status acp.ToolCallStatus) {
	writeSessionUpdate(stdout, sessionID, map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    toolCallID,
		"title":         title,
		"kind":          acp.ToolKindExecute,
		"status":        status,
		"rawInput": map[string]any{
			"cmd": "ls",
		},
	})
}

func writeToolCallUpdate(stdout *os.File, sessionID, toolCallID string, status acp.ToolCallStatus, rawOutput map[string]any) {
	writeSessionUpdate(stdout, sessionID, map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    toolCallID,
		"status":        status,
		"rawOutput":     rawOutput,
	})
}

func writeSessionUpdate(stdout *os.File, sessionID string, update map[string]any) {
	writeEnvelope(stdout, helperEnvelope{
		JSONRPC: "2.0",
		Method:  acp.ClientMethodSessionUpdate,
		Params: mustJSON(map[string]any{
			"sessionId": sessionID,
			"update":    update,
		}),
	})
}

func writeEnvelope(stdout *os.File, msg helperEnvelope) {
	must(json.NewEncoder(stdout).Encode(msg))
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	must(err)
	return data
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func compactJSONForCompare(raw []byte) string {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return out.String()
}

type helperEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *helperError    `json:"error,omitempty"`
}

type helperError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type helperInitializeResponse struct {
	ProtocolVersion int `json:"protocolVersion"`
}

type helperInitializeRequest struct {
	ClientInfo helperImplementation `json:"clientInfo"`
}

type helperImplementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type helperNewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type helperNewSessionRequest struct {
	Meta       map[string]any  `json:"_meta,omitempty"`
	Cwd        string          `json:"cwd"`
	McpServers []acp.McpServer `json:"mcpServers,omitempty"`
}

type helperPromptResponse struct {
	StopReason string `json:"stopReason"`
}

type helperSetSessionModelRequest struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type helperSetSessionModelResponse struct{}

type helperSetSessionModeRequest struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type helperSetSessionModeResponse struct{}

type helperPromptRequest struct {
	SessionID string              `json:"sessionId"`
	Prompt    []helperContentPart `json:"prompt"`
}

type helperContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type helperPermissionRequest struct {
	SessionID string                   `json:"sessionId"`
	Options   []helperPermissionOption `json:"options"`
	ToolCall  helperPermissionToolCall `json:"toolCall"`
}

type helperPermissionOption struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	OptionID string `json:"optionId"`
}

type helperPermissionToolCall struct {
	Title *string `json:"title,omitempty"`
}

type helperPermissionResponse struct {
	Outcome helperPermissionOutcome `json:"outcome"`
}

type helperPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

func readPromptOutput(t *testing.T, updates <-chan ExtendedSessionNotification, resultCh <-chan PromptResult) string {
	t.Helper()
	var chunks []string
	for note := range updates {
		ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", ExtendedSessionNotification{SessionNotification: note.SessionNotification, Raw: note.Raw})
		if ok {
			if text := extractPromptText(ev.Content); text != "" {
				chunks = append(chunks, text)
			}
		}
	}
	result := <-resultCh
	if result.Err != nil {
		t.Fatalf("PromptResult.Err = %v", result.Err)
	}
	return strings.Join(chunks, "")
}

func TestMapACPPlanUpdate(t *testing.T) {
	logger := zerolog.Nop()

	tests := []struct {
		name      string
		plan      *acp.SessionUpdatePlan
		wantOK    bool
		wantCount int
	}{
		{
			name:   "nil plan",
			plan:   nil,
			wantOK: false,
		},
		{
			name:   "empty entries",
			plan:   &acp.SessionUpdatePlan{Entries: []acp.PlanEntry{}},
			wantOK: false,
		},
		{
			name: "single entry",
			plan: &acp.SessionUpdatePlan{
				Entries: []acp.PlanEntry{
					{
						Content:  "Run tests",
						Status:   acp.PlanEntryStatusInProgress,
						Priority: acp.PlanEntryPriorityMedium,
					},
				},
			},
			wantOK:    true,
			wantCount: 1,
		},
		{
			name: "multiple entries",
			plan: &acp.SessionUpdatePlan{
				Entries: []acp.PlanEntry{
					{Content: "Step 1", Status: acp.PlanEntryStatusCompleted},
					{Content: "Step 2", Status: acp.PlanEntryStatusPending},
				},
			},
			wantOK:    true,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := mapACPPlanUpdate(logger, "inv-1", tt.plan)
			if ok != tt.wantOK {
				t.Errorf("mapACPPlanUpdate() ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && ev == nil {
				t.Errorf("mapACPPlanUpdate() ev = nil, want event")
			}
			if tt.wantOK && ev.Content != nil && len(ev.Content.Parts) != tt.wantCount {
				t.Errorf("mapACPPlanUpdate() parts = %d, want %d", len(ev.Content.Parts), tt.wantCount)
			}
		})
	}
}
