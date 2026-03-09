package acpagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
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

type badJSONMarshaler struct{}

func (badJSONMarshaler) MarshalJSON() ([]byte, error) {
	return []byte("{"), nil
}

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
	sess, err := client.NewSession(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates, resultCh, err := client.Prompt(context.Background(), string(sess.SessionId), "hello")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	var chunks []string
	for note := range updates {
		if text := updateText(note.Update); text != "" {
			chunks = append(chunks, text)
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
	sess, err := client.CreateSession(context.Background(), t.TempDir(), "openai/gpt-5.4")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(sess.SessionId)); got == "" {
		t.Fatal("CreateSession() returned empty session id")
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
	sess, err := client.NewSession(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	updates, resultCh, err := client.Prompt(context.Background(), string(sess.SessionId), "permission")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	var chunks []string
	for note := range updates {
		if text := updateText(note.Update); text != "" {
			chunks = append(chunks, text)
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
	sess1, err := client.NewSession(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	sess2, err := client.NewSession(context.Background(), t.TempDir())
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
	sess, err := client.NewSession(context.Background(), t.TempDir())
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
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

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

func TestClientLogsLastChunkInSeries(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	client := &Client{
		logger: logger,
		activeBySession: map[acp.SessionId]*activePrompt{
			"session-1": {
				sessionID: "session-1",
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

func collectFinalText(t *testing.T, events iter.Seq2[*session.Event, error]) string {
	t.Helper()
	var partial strings.Builder
	final := ""
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
		if text == "" {
			continue
		}
		if ev.Partial {
			partial.WriteString(text)
			continue
		}
		final = text
	}
	if final == "" {
		final = partial.String()
	}
	if !turnCompleteSeen {
		t.Fatalf("expected turn complete event")
	}
	return final
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

	var partialText strings.Builder
	var finalText strings.Builder
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
		if text == "" {
			continue
		}
		if ev.Partial {
			partialText.WriteString(text)
			continue
		}
		finalText.WriteString(text)
	}
	if !turnCompleteSeen {
		t.Fatalf("expected turn complete event")
	}
	got := partialText.String() + finalText.String()
	if got != "session-1:hello" {
		t.Fatalf("combined streamed text = %q, want %q", got, "session-1:hello")
	}
}


func TestMapACPUpdateToEventAgentMessageChunk(t *testing.T) {
	ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", acp.UpdateAgentMessageText("hello"))
	if !ok || ev == nil {
		t.Fatalf("mapACPUpdateToEvent() returned no event")
	}
	if !ev.Partial {
		t.Fatalf("event.Partial = false, want true")
	}
	if got := extractPromptText(ev.Content); got != "hello" {
		t.Fatalf("event text = %q, want %q", got, "hello")
	}
}

func TestMapACPUpdateToEventToolCall(t *testing.T) {
	ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", acp.StartToolCall(
		acp.ToolCallId(testACPCallID),
		"run shell",
		acp.WithStartKind(acp.ToolKindExecute),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
		acp.WithStartRawInput(map[string]any{"cmd": "ls"}),
	))
	if !ok || ev == nil {
		t.Fatalf("mapACPUpdateToEvent() returned no event")
	}
	if ev.Content == nil || len(ev.Content.Parts) != 1 || ev.Content.Parts[0].FunctionCall == nil {
		t.Fatalf("event content = %+v, want single function call part", ev.Content)
	}
	call := ev.Content.Parts[0].FunctionCall
	if call.ID != testACPCallID {
		t.Fatalf("function call id = %q, want %q", call.ID, testACPCallID)
	}
	if call.Name != "acp_tool_call" {
		t.Fatalf("function call name = %q, want %q", call.Name, "acp_tool_call")
	}
	if len(ev.LongRunningToolIDs) != 1 || ev.LongRunningToolIDs[0] != testACPCallID {
		t.Fatalf("long running ids = %v, want [%s]", ev.LongRunningToolIDs, testACPCallID)
	}
}

func TestMapACPUpdateToEventToolCallUpdate(t *testing.T) {
	ev, ok := mapACPUpdateToEvent(zerolog.Nop(), "inv-1", acp.UpdateToolCall(
		acp.ToolCallId(testACPCallID),
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateRawOutput(map[string]any{"ok": true}),
	))
	if !ok || ev == nil {
		t.Fatalf("mapACPUpdateToEvent() returned no event")
	}
	if ev.Content == nil || len(ev.Content.Parts) != 1 || ev.Content.Parts[0].FunctionResponse == nil {
		t.Fatalf("event content = %+v, want single function response part", ev.Content)
	}
	resp := ev.Content.Parts[0].FunctionResponse
	if resp.ID != testACPCallID {
		t.Fatalf("function response id = %q, want %q", resp.ID, testACPCallID)
	}
	if resp.Name != "acp_tool_call_update" {
		t.Fatalf("function response name = %q, want %q", resp.Name, "acp_tool_call_update")
	}
	if len(ev.LongRunningToolIDs) != 0 {
		t.Fatalf("long running ids = %v, want empty", ev.LongRunningToolIDs)
	}
}

func TestMapACPUpdateToEventIgnoresUnknownUpdate(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	ev, ok := mapACPUpdateToEvent(logger, "inv-1", acp.SessionUpdate{})
	if ok || ev != nil {
		t.Fatalf("mapACPUpdateToEvent() = (%v, %v), want no event", ev, ok)
	}
	if got := logBuf.String(); !strings.Contains(got, "ignoring unsupported acp session update") {
		t.Fatalf("debug log = %q, want unsupported update message", got)
	}
}

func TestMapACPUpdateToEventIgnoresAvailableCommandsUpdate(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	ev, ok := mapACPUpdateToEvent(logger, "inv-1", acp.SessionUpdate{
		AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			AvailableCommands: []acp.AvailableCommand{
				{Name: "compact", Description: "compact the session"},
			},
		},
	})
	if ok || ev != nil {
		t.Fatalf("mapACPUpdateToEvent() = (%v, %v), want no event", ev, ok)
	}
	got := logBuf.String()
	if !strings.Contains(got, "available_commands_update") {
		t.Fatalf("debug log = %q, want available_commands_update marker", got)
	}
	if !strings.Contains(got, "ignoring non-user-visible acp session update") {
		t.Fatalf("debug log = %q, want ignored update message", got)
	}
}

func TestMapACPUpdateToEventIgnoresUnmarshalableContentBlock(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	update := acp.SessionUpdate{
		UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
			Content: acp.ContentBlock{
				ResourceLink: &acp.ContentBlockResourceLink{
					Meta: badJSONMarshaler{},
					Name: "doc",
					Uri:  "file:///tmp/doc.txt",
				},
			},
		},
	}

	ev, ok := mapACPUpdateToEvent(logger, "inv-1", update)
	if ok || ev != nil {
		t.Fatalf("mapACPUpdateToEvent() = (%v, %v), want no event", ev, ok)
	}
	got := logBuf.String()
	if !strings.Contains(got, "resource_link") {
		t.Fatalf("debug log = %q, want resource_link marker", got)
	}
	if !strings.Contains(got, "\"acp_content_block\"") {
		t.Fatalf("debug log = %q, want structured content block payload", got)
	}
	if !strings.Contains(got, "\"acp_content_block_text\":\"resource_link name=\\\"doc\\\" uri=\\\"file:///tmp/doc.txt\\\"\"") {
		t.Fatalf("debug log = %q, want text content block summary", got)
	}
	if !strings.Contains(got, "\"name\":\"doc\"") {
		t.Fatalf("debug log = %q, want content block fields", got)
	}
	if !strings.Contains(got, "ignoring non-text acp content block") {
		t.Fatalf("debug log = %q, want ignored non-text block message", got)
	}
	if strings.Contains(got, "ignoring acp payload that failed to marshal") {
		t.Fatalf("debug log = %q, want no marshal failure", got)
	}
}

func TestMapACPUpdateToEventIgnoresUnknownContentBlockWithoutMarshalAttempt(t *testing.T) {
	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)

	update := acp.SessionUpdate{
		UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
			Content: acp.ContentBlock{},
		},
	}

	ev, ok := mapACPUpdateToEvent(logger, "inv-1", update)
	if ok || ev != nil {
		t.Fatalf("mapACPUpdateToEvent() = (%v, %v), want no event", ev, ok)
	}
	got := logBuf.String()
	if !strings.Contains(got, "ignoring unsupported acp content block") {
		t.Fatalf("debug log = %q, want unsupported content block message", got)
	}
	if !strings.Contains(got, "\"acp_content_block_text\":\"unknown\"") {
		t.Fatalf("debug log = %q, want unknown content block text", got)
	}
	if !strings.Contains(got, "\"acp_content_block\":{\"type\":\"unknown\"}") {
		t.Fatalf("debug log = %q, want structured unknown content block payload", got)
	}
	if strings.Contains(got, "failed to marshal") {
		t.Fatalf("debug log = %q, want no marshal failure", got)
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
	expectedClientName := os.Getenv("GO_EXPECT_CLIENT_NAME")
	expectedClientVersion := os.Getenv("GO_EXPECT_CLIENT_VERSION")
	expectedSessionModel := os.Getenv("GO_EXPECT_SESSION_MODEL")

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
			sessionCount++
			sessionID := fmt.Sprintf("session-%d", sessionCount)
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperNewSessionResponse{SessionID: sessionID})})
		case acp.AgentMethodSessionSetModel:
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
		case acp.AgentMethodSessionPrompt:
			var req helperPromptRequest
			must(json.Unmarshal(msg.Params, &req))
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

type helperPromptResponse struct {
	StopReason string `json:"stopReason"`
}

type helperSetSessionModelRequest struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type helperSetSessionModelResponse struct{}

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

func readPromptOutput(t *testing.T, updates <-chan acp.SessionNotification, resultCh <-chan PromptResult) string {
	t.Helper()
	var chunks []string
	for note := range updates {
		if text := updateText(note.Update); text != "" {
			chunks = append(chunks, text)
		}
	}
	result := <-resultCh
	if result.Err != nil {
		t.Fatalf("PromptResult.Err = %v", result.Err)
	}
	return strings.Join(chunks, "")
}
