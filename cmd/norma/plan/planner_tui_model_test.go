package plancmd

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestStatusFromEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ev   *session.Event
		want string
	}{
		{
			name: "nil event",
			ev:   nil,
			want: "Waiting for agent updates...",
		},
		{
			name: "function call title",
			ev: eventWithPart(&genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: "tool_call",
					Args: map[string]any{"title": "Inspect repo"},
				},
			}),
			want: "Running tool: Inspect repo...",
		},
		{
			name: "function call name fallback",
			ev: eventWithPart(&genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: "beads",
				},
			}),
			want: "Running tool: beads...",
		},
		{
			name: "function response title",
			ev: eventWithPart(&genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     "tool_update",
					Response: map[string]any{"title": "Inspect repo"},
				},
			}),
			want: "Tool finished: Inspect repo",
		},
		{
			name: "function response name fallback",
			ev: eventWithPart(&genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name: "beads",
				},
			}),
			want: "Tool finished: beads",
		},
		{
			name: "partial event",
			ev:   partialEvent(),
			want: "Agent is typing...",
		},
		{
			name: "turn complete",
			ev:   turnCompleteEvent(),
			want: "Waiting for next step...",
		},
		{
			name: "default status",
			ev:   &session.Event{},
			want: "Thinking...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := statusFromEvent(tc.ev)
			if got != tc.want {
				t.Fatalf("statusFromEvent() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlannerModel_StreamClosedQuits(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)

	_, cmd := model.Update(eventStreamClosedMsg{})
	assertQuitCmd(t, cmd)

	model = newTestModel(t)
	_, cmd = model.Update(questionStreamClosedMsg{})
	assertQuitCmd(t, cmd)
}

func TestPlannerModel_CompletedAndFailedQuit(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	updated, cmd := model.Update(planCompletedMsg("done"))
	updatedModel, ok := updated.(*plannerModel)
	if !ok {
		t.Fatalf("updated model type = %T, want *plannerModel", updated)
	}
	if updatedModel.completedRunMsg != "done" {
		t.Fatalf("completedRunMsg = %q, want %q", updatedModel.completedRunMsg, "done")
	}
	assertQuitCmd(t, cmd)

	model = newTestModel(t)
	updated, cmd = model.Update(planFailedMsg("boom"))
	updatedModel, ok = updated.(*plannerModel)
	if !ok {
		t.Fatalf("updated model type = %T, want *plannerModel", updated)
	}
	if updatedModel.failedRunError != "boom" {
		t.Fatalf("failedRunError = %q, want %q", updatedModel.failedRunError, "boom")
	}
	assertQuitCmd(t, cmd)
}

func eventWithPart(part *genai.Part) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	ev.Content = genai.NewContentFromParts([]*genai.Part{part}, genai.RoleModel)
	return ev
}

func partialEvent() *session.Event {
	ev := session.NewEvent(context.Background(), "inv-partial")
	ev.Partial = true
	return ev
}

func turnCompleteEvent() *session.Event {
	ev := session.NewEvent(context.Background(), "inv-complete")
	ev.TurnComplete = true
	return ev
}

func newTestModel(t *testing.T) *plannerModel {
	t.Helper()
	eventChan := make(chan *session.Event)
	questionChan := make(chan string)
	responseChan := make(chan string)
	model, err := newPlannerModel(eventChan, questionChan, responseChan, nil)
	if err != nil {
		t.Fatalf("newPlannerModel() error = %v", err)
	}
	return model
}

func assertQuitCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatalf("cmd = nil, want quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("cmd() message type = %T, want tea.QuitMsg", msg)
	}
}
