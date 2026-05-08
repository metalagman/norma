package adk

import (
	"context"
	"fmt"
	"iter"
	"testing"

	taskmaster "github.com/normahq/norma/pkg/runtime/taskmaster"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestWrapReusesSessionByTaskSessionID(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t)
	defer func() { _ = runner.Close() }()

	first, err := runner.RunTask(context.Background(), taskmaster.Task{SessionID: "session-a", Content: "first"})
	if err != nil {
		t.Fatalf("RunTask(first) error = %v", err)
	}
	second, err := runner.RunTask(context.Background(), taskmaster.Task{SessionID: "session-a", Content: "second"})
	if err != nil {
		t.Fatalf("RunTask(second) error = %v", err)
	}

	if first != "echo:first #1" {
		t.Fatalf("first output = %q, want echo:first #1", first)
	}
	if second != "echo:second #2" {
		t.Fatalf("second output = %q, want echo:second #2", second)
	}
}

func TestWrapIsolatesDistinctSessions(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t)
	defer func() { _ = runner.Close() }()

	first, err := runner.RunTask(context.Background(), taskmaster.Task{SessionID: "session-a", Content: "first"})
	if err != nil {
		t.Fatalf("RunTask(first) error = %v", err)
	}
	second, err := runner.RunTask(context.Background(), taskmaster.Task{SessionID: "session-b", Content: "second"})
	if err != nil {
		t.Fatalf("RunTask(second) error = %v", err)
	}

	if first != "echo:first #1" {
		t.Fatalf("first output = %q, want echo:first #1", first)
	}
	if second != "echo:second #1" {
		t.Fatalf("second output = %q, want echo:second #1", second)
	}
}

func TestWrapCloseClosesInnerAgent(t *testing.T) {
	t.Parallel()

	inner, err := newCountingAgent()
	if err != nil {
		t.Fatalf("newCountingAgent() error = %v", err)
	}
	closable := &closableAgent{Agent: inner}
	runner, err := Wrap(closable, Config{
		AppName: "taskmaster-test",
		UserID:  "root",
	})
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	if err := runner.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !closable.closed {
		t.Fatal("Close() did not close the wrapped agent")
	}
}

func newTestRunner(t *testing.T) taskmaster.LocalRunner {
	t.Helper()

	inner, err := newCountingAgent()
	if err != nil {
		t.Fatalf("newCountingAgent() error = %v", err)
	}
	runner, err := Wrap(inner, Config{
		AppName:      "taskmaster-test",
		UserID:       "root",
		SessionState: map[string]any{"prefix": "echo:"},
	})
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	return runner
}

type closableAgent struct {
	agent.Agent
	closed bool
}

func (a *closableAgent) Close() error {
	a.closed = true
	return nil
}

func newCountingAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "counting-agent",
		Description: "Test agent for taskmaster/adk",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				count := 1
				if value, err := ctx.Session().State().Get("count"); err == nil {
					if existing, ok := value.(int); ok {
						count = existing + 1
					}
				}

				prefix := ""
				if value, err := ctx.Session().State().Get("prefix"); err == nil {
					if existing, ok := value.(string); ok {
						prefix = existing
					}
				}

				text := prefix + userText(ctx.UserContent()) + fmt.Sprintf(" #%d", count)
				yield(&session.Event{
					LLMResponse: model.LLMResponse{
						Content: genai.NewContentFromText(text, genai.RoleModel),
					},
					Actions: session.EventActions{
						StateDelta: map[string]any{"count": count},
					},
				}, nil)
			}
		},
	})
}

func userText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	for _, part := range content.Parts {
		if part != nil && part.Text != "" {
			return part.Text
		}
	}
	return ""
}
