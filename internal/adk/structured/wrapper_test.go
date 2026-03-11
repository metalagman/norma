package structured

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"iter"
)

const (
	validStructuredOutputJSON = `{"output":"done"}`
)

func TestNewAgent_RequiresWrapped(t *testing.T) {
	t.Parallel()

	_, err := NewAgent(nil)
	if err == nil || !strings.Contains(err.Error(), "wrapped agent is required") {
		t.Fatalf("error = %v, want wrapped agent error", err)
	}
}

func TestNewAgent_UsesDefaultsWhenSchemasEmpty(t *testing.T) {
	t.Parallel()

	inner := newStaticOutputAgent(t, validStructuredOutputJSON, nil)

	if _, err := NewAgent(inner, WithInputSchema("")); err != nil {
		t.Fatalf("NewAgent() with empty input schema error = %v", err)
	}
	if _, err := NewAgent(inner, WithOutputSchema("")); err != nil {
		t.Fatalf("NewAgent() with empty output schema error = %v", err)
	}
}

func TestNewAgent_RejectsInvalidSchemas(t *testing.T) {
	t.Parallel()

	inner := newStaticOutputAgent(t, validStructuredOutputJSON, nil)

	_, err := NewAgent(inner, WithInputSchema("{"))
	if err == nil || !strings.Contains(err.Error(), "input schema invalid") {
		t.Fatalf("error = %v, want invalid input schema error", err)
	}

	_, err = NewAgent(inner, WithOutputSchema("{"))
	if err == nil || !strings.Contains(err.Error(), "output schema invalid") {
		t.Fatalf("error = %v, want invalid output schema error", err)
	}
}

func TestWrapperAgentValidationCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		input             string
		output            string
		wantErrContains   string
		wantOutputContain string
		wantInnerCalls    int32
	}{
		{
			name:            "invalid_input",
			input:           "not-json",
			output:          validStructuredOutputJSON,
			wantErrContains: "validate structured input",
			wantInnerCalls:  0,
		},
		{
			name:            "invalid_output",
			input:           `{"input":"hello"}`,
			output:          `{"status":"ok"}`,
			wantErrContains: "validate structured output",
			wantInnerCalls:  1,
		},
		{
			name:              "valid_input_output",
			input:             `{"input":"hello"}`,
			output:            validStructuredOutputJSON,
			wantOutputContain: `"output":"done"`,
			wantInnerCalls:    1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var called int32
			inner := newStaticOutputAgent(t, tc.output, &called)
			wrapped, err := NewAgent(inner)
			if err != nil {
				t.Fatalf("NewAgent() error = %v", err)
			}

			out, runErr := runSingleTurn(t, wrapped, tc.input)
			if tc.wantErrContains != "" {
				if runErr == nil {
					t.Fatalf("runSingleTurn() expected error containing %q", tc.wantErrContains)
				}
				if got := runErr.Error(); !strings.Contains(got, tc.wantErrContains) {
					t.Fatalf("error = %q, want contains %q", got, tc.wantErrContains)
				}
			} else if runErr != nil {
				t.Fatalf("runSingleTurn() error = %v", runErr)
			}

			if tc.wantOutputContain != "" && !strings.Contains(out, tc.wantOutputContain) {
				t.Fatalf("output = %q, want contains %q", out, tc.wantOutputContain)
			}
			if got := atomic.LoadInt32(&called); got != tc.wantInnerCalls {
				t.Fatalf("inner called = %d, want %d", got, tc.wantInnerCalls)
			}
		})
	}
}

func TestContentText_IgnoresThoughtChunks(t *testing.T) {
	t.Parallel()

	content := genai.NewContentFromParts([]*genai.Part{
		{Text: "visible-1"},
		{Text: "hidden-thought", Thought: true},
		{Text: "visible-2"},
	}, genai.RoleModel)

	got := contentText(content)
	if got != "visible-1visible-2" {
		t.Fatalf("contentText() = %q, want %q", got, "visible-1visible-2")
	}
}

func TestWrapperAgentAppendsTurnCompleteWhenMissing(t *testing.T) {
	t.Parallel()

	inner, err := adkagent.New(adkagent.Config{
		Name:        "NoTurnCompleteInner",
		Description: "Inner agent without turn complete event",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx.InvocationID())
				ev.Content = genai.NewContentFromText(validStructuredOutputJSON, genai.RoleModel)
				_ = yield(ev, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	wrapped, err := NewAgent(inner)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	out, turnCompleteCount, runErr := runSingleTurnWithMeta(t, wrapped, `{"input":"hello"}`)
	if runErr != nil {
		t.Fatalf("runSingleTurnWithMeta() error = %v", runErr)
	}
	if strings.TrimSpace(out) != validStructuredOutputJSON {
		t.Fatalf("output = %q, want %q", out, validStructuredOutputJSON)
	}
	if turnCompleteCount != 1 {
		t.Fatalf("turnCompleteCount = %d, want 1", turnCompleteCount)
	}
}

func TestWrapperAgentStopsCollectingAfterTurnComplete(t *testing.T) {
	t.Parallel()

	inner, err := adkagent.New(adkagent.Config{
		Name:        "AfterTurnCompleteInner",
		Description: "Inner agent with post-turncomplete event",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				first := session.NewEvent(ctx.InvocationID())
				first.Content = genai.NewContentFromText(validStructuredOutputJSON, genai.RoleModel)
				if !yield(first, nil) {
					return
				}

				done := session.NewEvent(ctx.InvocationID())
				done.TurnComplete = true
				if !yield(done, nil) {
					return
				}

				late := session.NewEvent(ctx.InvocationID())
				late.Content = genai.NewContentFromText("late", genai.RoleModel)
				_ = yield(late, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	wrapped, err := NewAgent(inner)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	out, turnCompleteCount, runErr := runSingleTurnWithMeta(t, wrapped, `{"input":"hello"}`)
	if runErr != nil {
		t.Fatalf("runSingleTurnWithMeta() error = %v", runErr)
	}
	if strings.TrimSpace(out) != validStructuredOutputJSON {
		t.Fatalf("output = %q, want %q", out, validStructuredOutputJSON)
	}
	if turnCompleteCount != 1 {
		t.Fatalf("turnCompleteCount = %d, want 1", turnCompleteCount)
	}
}

func TestWrapperAgentPassesNonAccumulatedEventsWithoutBuffering(t *testing.T) {
	t.Parallel()

	inner, err := adkagent.New(adkagent.Config{
		Name:        "PassthroughBeforeInvalidOutputInner",
		Description: "Inner agent with passthrough event before invalid text output",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				passthrough := session.NewEvent(ctx.InvocationID())
				passthrough.Author = "passthrough"
				if !yield(passthrough, nil) {
					return
				}

				invalid := session.NewEvent(ctx.InvocationID())
				invalid.Content = genai.NewContentFromText("not-json", genai.RoleModel)
				if !yield(invalid, nil) {
					return
				}

				done := session.NewEvent(ctx.InvocationID())
				done.TurnComplete = true
				_ = yield(done, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	wrapped, err := NewAgent(inner)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "structured-wrapper-test",
		Agent:          wrapped,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}

	const userID = "test-user"
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "structured-wrapper-test",
		UserID:  userID,
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	sawPassthrough := false
	var runErr error
	events := r.Run(
		context.Background(),
		userID,
		sess.Session.ID(),
		genai.NewContentFromText(`{"input":"hello"}`, genai.RoleUser),
		adkagent.RunConfig{},
	)
	for ev, err := range events {
		if err != nil {
			runErr = err
			break
		}
		if ev != nil && ev.Author == "passthrough" {
			sawPassthrough = true
		}
	}

	if runErr == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(runErr.Error(), "validate structured output") {
		t.Fatalf("error = %v, want validate structured output", runErr)
	}
	if !sawPassthrough {
		t.Fatal("expected passthrough event to be yielded before validation error")
	}
}

func TestWrapperAgentRejectsAccumulatedOutputOverLimit(t *testing.T) {
	t.Parallel()

	largeOutput := `{"output":"` + strings.Repeat("a", 64) + `"}`
	inner := newStaticOutputAgent(t, largeOutput, nil)
	wrapped, err := NewAgent(inner, WithMaxAccumulatedOutputBytes(16))
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
	if runErr == nil {
		t.Fatal("expected accumulated output limit error, got nil")
	}
	if got := runErr.Error(); !strings.Contains(got, "accumulated output exceeds limit") {
		t.Fatalf("error = %q, want accumulated output limit error", got)
	}
}

func newStaticOutputAgent(t *testing.T, output string, called *int32) adkagent.Agent {
	t.Helper()

	a, err := adkagent.New(adkagent.Config{
		Name:        "StubInnerAgent",
		Description: "Stub inner agent",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				if called != nil {
					atomic.AddInt32(called, 1)
				}
				ev := session.NewEvent(ctx.InvocationID())
				ev.Content = genai.NewContentFromText(output, genai.RoleModel)
				if !yield(ev, nil) {
					return
				}

				final := session.NewEvent(ctx.InvocationID())
				final.TurnComplete = true
				_ = yield(final, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	return a
}

func runSingleTurn(t *testing.T, a adkagent.Agent, input string) (string, error) {
	t.Helper()

	out, _, err := runSingleTurnWithMeta(t, a, input)
	return out, err
}

func runSingleTurnWithMeta(t *testing.T, a adkagent.Agent, input string) (string, int, error) {
	t.Helper()

	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "structured-wrapper-test",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}

	const userID = "test-user"
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "structured-wrapper-test",
		UserID:  userID,
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	var out strings.Builder
	turnCompleteCount := 0
	events := r.Run(
		context.Background(),
		userID,
		sess.Session.ID(),
		genai.NewContentFromText(input, genai.RoleUser),
		adkagent.RunConfig{},
	)
	for ev, runErr := range events {
		if runErr != nil {
			return out.String(), turnCompleteCount, runErr
		}
		if ev == nil {
			continue
		}
		if ev.TurnComplete {
			turnCompleteCount++
		}
		if ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part == nil || part.Text == "" {
				continue
			}
			out.WriteString(part.Text)
		}
	}
	return out.String(), turnCompleteCount, nil
}
