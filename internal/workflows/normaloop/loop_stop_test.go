package normaloop

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type testInvocationContext struct {
	context.Context
	sess  session.Session
	ended bool
}

func (c *testInvocationContext) Agent() agent.Agent          { return nil }
func (c *testInvocationContext) Artifacts() agent.Artifacts  { return nil }
func (c *testInvocationContext) Memory() agent.Memory        { return nil }
func (c *testInvocationContext) Session() session.Session    { return c.sess }
func (c *testInvocationContext) InvocationID() string        { return "inv-test" }
func (c *testInvocationContext) Branch() string              { return "" }
func (c *testInvocationContext) UserContent() *genai.Content { return nil }
func (c *testInvocationContext) RunConfig() *agent.RunConfig { return nil }
func (c *testInvocationContext) EndInvocation()              { c.ended = true }
func (c *testInvocationContext) Ended() bool                 { return c.ended }

func newTestSubAgent(t *testing.T, name string, run func(agent.InvocationContext)) agent.Agent {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Name:        name,
		Description: "test sub-agent",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(func(*session.Event, error) bool) {
				run(ctx)
			}
		},
	})
	if err != nil {
		t.Fatalf("create test sub-agent %q: %v", name, err)
	}
	return ag
}

func TestRunStopsAfterActWhenStopFlagSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessionService := session.InMemoryService()
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  "test-user",
		State: map[string]any{
			"iteration": 1,
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var planCalls, doCalls, checkCalls, actCalls int

	orchestrator := &NormaLoopAgent{
		planAgent: newTestSubAgent(t, "plan", func(agent.InvocationContext) { planCalls++ }),
		doAgent:   newTestSubAgent(t, "do", func(agent.InvocationContext) { doCalls++ }),
		checkAgent: newTestSubAgent(t, "check", func(agent.InvocationContext) {
			checkCalls++
		}),
		actAgent: newTestSubAgent(t, "act", func(ic agent.InvocationContext) {
			actCalls++
			if err := ic.Session().State().Set("stop", true); err != nil {
				t.Fatalf("set stop flag: %v", err)
			}
		}),
	}

	invocationCtx := &testInvocationContext{
		Context: ctx,
		sess:    created.Session,
	}

	for _, runErr := range orchestrator.Run(invocationCtx) {
		if runErr != nil {
			t.Fatalf("run orchestrator: %v", runErr)
		}
	}

	if planCalls != 1 || doCalls != 1 || checkCalls != 1 || actCalls != 1 {
		t.Fatalf("unexpected call counts: plan=%d do=%d check=%d act=%d", planCalls, doCalls, checkCalls, actCalls)
	}

	iteration, err := created.Session.State().Get("iteration")
	if err != nil {
		t.Fatalf("get iteration: %v", err)
	}
	if got, ok := iteration.(int); !ok || got != 1 {
		t.Fatalf("iteration = %#v, want 1", iteration)
	}
}

func TestRunExitsImmediatelyWhenAlreadyStopped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sessionService := session.InMemoryService()
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  "test-user",
		State: map[string]any{
			"iteration": 4,
			"stop":      true,
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var called int
	sub := newTestSubAgent(t, "sub", func(agent.InvocationContext) { called++ })
	orchestrator := &NormaLoopAgent{
		planAgent:  sub,
		doAgent:    sub,
		checkAgent: sub,
		actAgent:   sub,
	}

	invocationCtx := &testInvocationContext{
		Context: ctx,
		sess:    created.Session,
	}

	for _, runErr := range orchestrator.Run(invocationCtx) {
		if runErr != nil {
			t.Fatalf("run orchestrator: %v", runErr)
		}
	}

	if called != 0 {
		t.Fatalf("expected no sub-agent calls when stopped, got %d", called)
	}

	iteration, err := created.Session.State().Get("iteration")
	if err != nil {
		t.Fatalf("get iteration: %v", err)
	}
	if got, ok := iteration.(int); !ok || got != 4 {
		t.Fatalf("iteration = %#v, want 4", iteration)
	}
}
