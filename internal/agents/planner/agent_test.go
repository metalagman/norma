package planner

import (
	"context"
	"iter"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestNewRejectsNilBase(t *testing.T) {
	t.Parallel()

	got, err := New(nil)
	if err == nil {
		t.Fatal("New(nil) expected error")
	}
	if got != nil {
		t.Fatalf("New(nil) = %v, want nil agent", got)
	}
}

func TestDecoratedAgentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base string
		want string
	}{
		{base: "opencode_acp_agent", want: "opencode_acp_planner"},
		{base: "opencode_acp", want: "opencode_acp_planner"},
		{base: "opencode_acp_planner", want: "opencode_acp_planner"},
		{base: " ", want: "planner"},
	}

	for _, tc := range tests {
		t.Run(tc.base, func(t *testing.T) {
			t.Parallel()
			if got := decoratedAgentName(tc.base); got != tc.want {
				t.Fatalf("decoratedAgentName(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

func TestPlannerWrapperInjectsInstructionPrompt(t *testing.T) {
	t.Parallel()

	var seenPrompt string
	base := mustNewTestAgent(t, "opencode_acp_agent", "base agent", func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
		seenPrompt = contentText(ctx.UserContent())
		return turnComplete(ctx.InvocationID())
	})

	wrapped, err := New(base)
	if err != nil {
		t.Fatalf("New(base) error = %v", err)
	}

	if wrapped.Name() != "opencode_acp_planner" {
		t.Fatalf("wrapped.Name() = %q, want %q", wrapped.Name(), "opencode_acp_planner")
	}
	if wrapped.Description() != "base agent" {
		t.Fatalf("wrapped.Description() = %q, want %q", wrapped.Description(), "base agent")
	}

	runAgentOnce(t, wrapped, "plan the epic")

	if !strings.Contains(seenPrompt, "You are Norma's planning agent.") {
		t.Fatalf("wrapped prompt missing planner instruction: %q", seenPrompt)
	}
	if !strings.Contains(seenPrompt, "plan the epic") {
		t.Fatalf("wrapped prompt missing user prompt: %q", seenPrompt)
	}
}

func TestPlannerWrapperCloseDelegatesToBase(t *testing.T) {
	t.Parallel()

	base := &closableAgent{
		Agent: mustNewTestAgent(t, "base_agent", "", func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return turnComplete(ctx.InvocationID())
		}),
	}

	wrapped, err := New(base)
	if err != nil {
		t.Fatalf("New(base) error = %v", err)
	}

	closer, ok := wrapped.(interface{ Close() error })
	if !ok {
		t.Fatalf("wrapped agent %T does not implement Close()", wrapped)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("wrapped.Close() error = %v", err)
	}
	if !base.closed {
		t.Fatal("base Close() was not called")
	}
}

type closableAgent struct {
	adkagent.Agent
	closed bool
}

func (a *closableAgent) Close() error {
	a.closed = true
	return nil
}

func mustNewTestAgent(
	t *testing.T,
	name string,
	desc string,
	run func(adkagent.InvocationContext) iter.Seq2[*session.Event, error],
) adkagent.Agent {
	t.Helper()
	ag, err := adkagent.New(adkagent.Config{
		Name:        name,
		Description: desc,
		Run:         run,
	})
	if err != nil {
		t.Fatalf("adkagent.New() error = %v", err)
	}
	return ag
}

func runAgentOnce(t *testing.T, ag adkagent.Agent, prompt string) {
	t.Helper()

	svc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "planner-test",
		Agent:          ag,
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}

	created, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "planner-test",
		UserID:  "planner-test-user",
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	events := r.Run(
		context.Background(),
		"planner-test-user",
		created.Session.ID(),
		genai.NewContentFromText(prompt, genai.RoleUser),
		adkagent.RunConfig{},
	)
	for _, runErr := range events {
		if runErr != nil {
			t.Fatalf("runner.Run() error = %v", runErr)
		}
	}
}

func turnComplete(invocationID string) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		ev := session.NewEvent(invocationID)
		ev.TurnComplete = true
		yield(ev, nil)
	}
}
