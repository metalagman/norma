package goalkeeper

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestNewRequiresWorker(t *testing.T) {
	t.Parallel()

	validator := mustNewTestAgent(t, "validator", func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(func(*session.Event, error) bool) {}
	})
	workflow, err := New(nil, validator)
	if err == nil {
		t.Fatalf("New(nil, validator) error = nil, want validation error")
	}
	if workflow != nil {
		t.Fatalf("New(nil, validator) workflow = %v, want nil", workflow)
	}
}

func TestNewRequiresValidator(t *testing.T) {
	t.Parallel()

	worker := mustNewTestAgent(t, "worker", func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(func(*session.Event, error) bool) {}
	})
	workflow, err := New(worker, nil)
	if err == nil {
		t.Fatalf("New(worker, nil) error = nil, want validation error")
	}
	if workflow != nil {
		t.Fatalf("New(worker, nil) workflow = %v, want nil", workflow)
	}
}

func TestWorkflowRunsWorkerThenValidatorWithSharedSession(t *testing.T) {
	t.Parallel()

	var order []string
	worker := mustNewTestAgent(t, "worker", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			order = append(order, "worker")
			if err := ctx.Session().State().Set("worker_result", "created artifact"); err != nil {
				yield(nil, err)
				return
			}
			yield(textEvent(ctx.InvocationID(), "worker result"), nil)
		}
	})
	validator := mustNewTestAgent(t, "validator", func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			order = append(order, "validator")
			value, err := ctx.Session().State().Get("worker_result")
			if err != nil {
				yield(nil, err)
				return
			}
			yield(textEvent(ctx.InvocationID(), "verdict: pass\nworker_result="+value.(string)), nil)
		}
	})

	workflow, err := New(worker, validator)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got := runTestAgentOnce(t, workflow, "Goal:\nship")
	if got != "verdict: pass\nworker_result=created artifact" {
		t.Fatalf("workflow output = %q, want validator output from shared state", got)
	}
	if strings.Join(order, ",") != "worker,validator" {
		t.Fatalf("order = %v, want worker then validator", order)
	}
}

func mustNewTestAgent(
	t *testing.T,
	name string,
	run func(agent.InvocationContext) iter.Seq2[*session.Event, error],
) agent.Agent {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Name:        name,
		Description: name + " test agent",
		Run:         run,
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	return ag
}

func runTestAgentOnce(t *testing.T, ag agent.Agent, prompt string) string {
	t.Helper()

	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        "goalkeeper-test",
		Agent:          ag,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	created, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "goalkeeper-test",
		UserID:  "goalkeeper-test-user",
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	var last string
	events := runner.Run(
		context.Background(),
		"goalkeeper-test-user",
		created.Session.ID(),
		genai.NewContentFromText(prompt, genai.RoleUser),
		agent.RunConfig{},
	)
	for ev, runErr := range events {
		if runErr != nil {
			t.Fatalf("runner.Run() error = %v", runErr)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		if text := contentText(ev.Content); text != "" {
			last = text
		}
	}
	return last
}

func textEvent(invocationID string, text string) *session.Event {
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromText(text, genai.RoleModel)
	return ev
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}
