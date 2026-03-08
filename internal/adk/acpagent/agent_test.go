package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"google.golang.org/adk/agent"
	runnerpkg "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
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
	final := ""
	for ev, err := range events {
		if err != nil {
			t.Fatalf("runner event error = %v", err)
		}
		if ev == nil || ev.Content == nil || ev.Partial {
			continue
		}
		final = extractPromptText(ev.Content)
	}
	return final
}

func helperCommand(t *testing.T) []string {
	t.Helper()
	return []string{"env", "GO_WANT_ACP_HELPER=1", os.Args[0], "-test.run=TestACPHelperProcess", "--"}
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

	for scanner.Scan() {
		var msg helperEnvelope
		must(json.Unmarshal(scanner.Bytes(), &msg))
		switch msg.Method {
		case acp.AgentMethodInitialize:
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperInitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber})})
		case acp.AgentMethodSessionNew:
			sessionCount++
			sessionID := fmt.Sprintf("session-%d", sessionCount)
			writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustJSON(helperNewSessionResponse{SessionID: sessionID})})
		case acp.AgentMethodSessionPrompt:
			var req helperPromptRequest
			must(json.Unmarshal(msg.Params, &req))
			prompt := req.Prompt[0].Text
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
	writeEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", Method: acp.ClientMethodSessionUpdate, Params: mustJSON(helperSessionNotification{
		SessionID: sessionID,
		Update: helperSessionUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       &helperContentPart{Type: "text", Text: text},
		},
	})})
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

type helperNewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type helperPromptResponse struct {
	StopReason string `json:"stopReason"`
}

type helperPromptRequest struct {
	SessionID string              `json:"sessionId"`
	Prompt    []helperContentPart `json:"prompt"`
}

type helperContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type helperSessionNotification struct {
	SessionID string              `json:"sessionId"`
	Update    helperSessionUpdate `json:"update"`
}

type helperSessionUpdate struct {
	SessionUpdate string             `json:"sessionUpdate"`
	Content       *helperContentPart `json:"content,omitempty"`
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
