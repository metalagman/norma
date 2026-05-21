package structuredagent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"iter"
)

const (
	validStructuredOutputJSON = `{"output":"done"}`
)

func TestSentinelErrors(t *testing.T) {
	t.Parallel()

	// Test invalid input returns input sentinel
	innerValid := newStaticOutputAgent(t, validStructuredOutputJSON, nil)
	wrapped, err := NewAgent(innerValid)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	_, runErr := runSingleTurn(t, wrapped, "not-json")
	if runErr == nil {
		t.Fatal("expected error for invalid input")
	}
	if !errors.Is(runErr, ErrStructuredInputSchemaValidation) {
		t.Fatalf("error = %v, want error satisfying ErrStructuredInputSchemaValidation", runErr)
	}
	if !errors.Is(runErr, ErrStructuredIOSchemaValidation) {
		t.Fatalf("error = %v, want error satisfying ErrStructuredIOSchemaValidation", runErr)
	}
	if errors.Is(runErr, ErrStructuredOutputSchemaValidation) {
		t.Fatal("error should not satisfy ErrStructuredOutputSchemaValidation for input validation failure")
	}

	// Test invalid output returns output sentinel
	innerInvalidOutput := newStaticOutputAgent(t, `{"status":"ok"}`, nil)
	wrapped2, err := NewAgent(innerInvalidOutput)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	_, runErr = runSingleTurn(t, wrapped2, `{"input":"hello"}`)
	if runErr == nil {
		t.Fatal("expected error for invalid output")
	}
	if !errors.Is(runErr, ErrStructuredOutputSchemaValidation) {
		t.Fatalf("error = %v, want error satisfying ErrStructuredOutputSchemaValidation", runErr)
	}
	if !errors.Is(runErr, ErrStructuredIOSchemaValidation) {
		t.Fatalf("error = %v, want error satisfying ErrStructuredIOSchemaValidation", runErr)
	}
	if errors.Is(runErr, ErrStructuredInputSchemaValidation) {
		t.Fatal("error should not satisfy ErrStructuredInputSchemaValidation for output validation failure")
	}
}

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

func TestBuildPrompt_IncludesStrictOutputContract(t *testing.T) {
	t.Parallel()

	prompt, err := buildPrompt(promptData{
		Input:        `{"input":"hello"}`,
		InputSchema:  `{"type":"object"}`,
		OutputSchema: `{"type":"object"}`,
	})
	if err != nil {
		t.Fatalf("buildPrompt() error = %v", err)
	}

	assertContainsAll(t, prompt,
		"STRICT OUTPUT CONTRACT:",
		"MUST return exactly one JSON object that matches the output schema.",
		"MUST start the JSON object with \"{\" at byte-start OR at the beginning of a new line (column 1).",
		"MUST NOT include any text before the JSON object.",
		"MUST NOT include markdown code fences",
		"Invalid example (DO NOT DO THIS):",
		"Valid example:",
	)
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
			wantInnerCalls:  2,
		},
		{
			name:              "valid_input_output",
			input:             `{"input":"hello"}`,
			output:            validStructuredOutputJSON,
			wantOutputContain: `"output":"done"`,
			wantInnerCalls:    1,
		},
		{
			name:              "valid_output_with_preface_and_newline_started_json",
			input:             `{"input":"hello"}`,
			output:            "analysis\n" + validStructuredOutputJSON,
			wantOutputContain: `"output":"done"`,
			wantInnerCalls:    1,
		},
		{
			name:              "output_with_trailing_backticks_succeeds",
			input:             `{"input":"hello"}`,
			output:            "analysis\n" + validStructuredOutputJSON + "\n```",
			wantOutputContain: `"output":"done"`,
			wantInnerCalls:    1,
		},
		{
			name:              "output_with_fenced_json_block_succeeds",
			input:             `{"input":"hello"}`,
			output:            "```json\n" + validStructuredOutputJSON + "\n```",
			wantOutputContain: `"output":"done"`,
			wantInnerCalls:    1,
		},
		{
			name:            "output_with_trailing_backticks_and_text_fails_validation",
			input:           `{"input":"hello"}`,
			output:          "analysis\n" + validStructuredOutputJSON + "\n```\nextra",
			wantErrContains: "validate structured output",
			wantInnerCalls:  2,
		},
		{
			name:            "output_without_line_started_json_payload",
			input:           `{"input":"hello"}`,
			output:          "analysis " + validStructuredOutputJSON,
			wantErrContains: "validate structured output",
			wantInnerCalls:  2,
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

func TestExtractOutputJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		want      string
		wantError string
	}{
		{
			name: "extract_from_byte_start",
			raw:  `{"output":"done"}`,
			want: `{"output":"done"}`,
		},
		{
			name: "extract_from_line_start_after_preface",
			raw:  "notes\n" + `{"output":"done"}`,
			want: `{"output":"done"}`,
		},
		{
			name: "extract_allows_whitespace_after_json",
			raw:  "notes\n" + `{"output":"done"}` + "\n  \t",
			want: `{"output":"done"}`,
		},
		{
			name: "extract_allows_markdown_closing_fence_after_json",
			raw:  "notes\n" + `{"output":"done"}` + "\n```",
			want: `{"output":"done"}`,
		},
		{
			name: "extract_allows_fenced_json_block",
			raw:  "```json\n" + `{"output":"done"}` + "\n```",
			want: `{"output":"done"}`,
		},
		{
			name:      "error_on_non_whitespace_after_json",
			raw:       "notes\n" + `{"output":"done"}` + "\nextra",
			wantError: "non-whitespace content after JSON object",
		},
		{
			name:      "error_on_non_whitespace_after_markdown_closing_fence",
			raw:       "notes\n" + `{"output":"done"}` + "\n```\nextra",
			wantError: "non-whitespace content after JSON object",
		},
		{
			name:      "error_on_multiple_markdown_closing_fences",
			raw:       "notes\n" + `{"output":"done"}` + "\n```\n```",
			wantError: "non-whitespace content after JSON object",
		},
		{
			name:      "error_when_no_line_started_json",
			raw:       "notes " + `{"output":"done"}`,
			wantError: "no JSON object found at byte start or line start",
		},
		{
			name:      "error_on_empty_output",
			raw:       " \n\t ",
			wantError: "output is empty",
		},
		{
			name:      "error_on_unterminated_json_object",
			raw:       "notes\n" + `{"output":"done"`,
			wantError: "unterminated JSON object",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := extractOutputJSON(tc.raw)
			if tc.wantError != "" {
				if err == nil {
					t.Fatalf("extractOutputJSON() error = nil, want contains %q", tc.wantError)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("extractOutputJSON() error = %q, want contains %q", err.Error(), tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractOutputJSON() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("extractOutputJSON() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWrapperAgentLogsOutputValidationFailureWithContextLogger(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := zerolog.New(&logBuf).Level(zerolog.DebugLevel)
	ctx := logger.WithContext(context.Background())

	inner := newStaticOutputAgent(t, "I am not JSON output", nil)
	wrapped, err := NewAgent(inner)
	if err != nil {
		t.Fatalf("NewAgent() error = %v", err)
	}

	_, _, runErr := runSingleTurnWithContext(t, ctx, wrapped, `{"input":"hello"}`)
	if runErr == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(runErr.Error(), "validate structured output") {
		t.Fatalf("error = %v, want validate structured output", runErr)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "output schema validation failed") {
		t.Fatalf("logs = %q, want validation failure message", logs)
	}
	if !strings.Contains(logs, "accumulated_output_preview") {
		t.Fatalf("logs = %q, want accumulated output preview field", logs)
	}
	if !strings.Contains(logs, "validation_json_full") {
		t.Fatalf("logs = %q, want validation_json_full field", logs)
	}
	if !strings.Contains(logs, "I am not JSON output") {
		t.Fatalf("logs = %q, want failing output preview", logs)
	}
}

func TestWrapperAgentIncludesTextFromTurnCompleteEvent(t *testing.T) {
	t.Parallel()

	inner, err := adkagent.New(adkagent.Config{
		Name:        "TurnCompleteWithTextInner",
		Description: "Inner agent emits final text on turn_complete event",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				ev := session.NewEvent(ctx.InvocationID())
				ev.Content = genai.NewContentFromText(validStructuredOutputJSON, genai.RoleModel)
				ev.TurnComplete = true
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

func TestWrapperAgentSuppressesPassthroughEvents(t *testing.T) {
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
	if sawPassthrough {
		t.Fatal("expected passthrough event to be suppressed")
	}
}

func TestWrapperAgentEmitsSingleJSONTextChunk(t *testing.T) {
	t.Parallel()

	inner, err := adkagent.New(adkagent.Config{
		Name:        "ChunkedOutputInner",
		Description: "Inner agent emits output across multiple text chunks",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				chunks := []string{
					"analysis before output\n",
					`{"output":"`,
					`done"}`,
				}
				for _, chunk := range chunks {
					ev := session.NewEvent(ctx.InvocationID())
					ev.Content = genai.NewContentFromText(chunk, genai.RoleModel)
					if !yield(ev, nil) {
						return
					}
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

	textChunkCount := 0
	turnCompleteCount := 0
	var out strings.Builder
	events := r.Run(
		context.Background(),
		userID,
		sess.Session.ID(),
		genai.NewContentFromText(`{"input":"hello"}`, genai.RoleUser),
		adkagent.RunConfig{},
	)
	for ev, runErr := range events {
		if runErr != nil {
			t.Fatalf("runner.Run() error = %v", runErr)
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
			textChunkCount++
			out.WriteString(part.Text)
		}
	}

	if got := strings.TrimSpace(out.String()); got != validStructuredOutputJSON {
		t.Fatalf("output = %q, want %q", got, validStructuredOutputJSON)
	}
	if textChunkCount != 1 {
		t.Fatalf("textChunkCount = %d, want 1", textChunkCount)
	}
	if turnCompleteCount != 1 {
		t.Fatalf("turnCompleteCount = %d, want 1", turnCompleteCount)
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
	return runSingleTurnWithContext(t, context.Background(), a, input)
}

func runSingleTurnWithContext(t *testing.T, ctx context.Context, a adkagent.Agent, input string) (string, int, error) {
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
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "structured-wrapper-test",
		UserID:  userID,
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	var out strings.Builder
	turnCompleteCount := 0
	events := r.Run(
		ctx,
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

func assertContainsAll(t *testing.T, got string, wantParts ...string) {
	t.Helper()
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("text does not contain %q; text=%q", part, got)
		}
	}
}

func newMultiOutputAgent(t *testing.T, outputs []string, calls *int32, runErr error) adkagent.Agent {
	t.Helper()

	outputIdx := 0
	a, err := adkagent.New(adkagent.Config{
		Name:        "MultiOutputAgent",
		Description: "Multi-output agent for retry testing",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				if calls != nil {
					atomic.AddInt32(calls, 1)
				}

				if runErr != nil {
					ev := session.NewEvent(ctx.InvocationID())
					if !yield(ev, runErr) {
						return
					}
					return
				}

				output := outputs[outputIdx]
				if outputIdx < len(outputs)-1 {
					outputIdx++
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

func TestRetryMatrix(t *testing.T) {
	t.Parallel()

	t.Run("invalid_input_no_retry", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		inner := newStaticOutputAgent(t, validStructuredOutputJSON, &callCount)
		wrapped, err := NewAgent(inner)
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, "not-json")
		if runErr == nil {
			t.Fatal("expected error for invalid input")
		}
		if !errors.Is(runErr, ErrStructuredInputSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredInputSchemaValidation, got %v", runErr)
		}
		if !errors.Is(runErr, ErrStructuredIOSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredIOSchemaValidation, got %v", runErr)
		}
		if atomic.LoadInt32(&callCount) != 0 {
			t.Errorf("inner agent should not be called for input validation failure, got %d calls", callCount)
		}
	})

	t.Run("invalid_output_then_valid_retries", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		invalidOutput := `{"invalid":"response"}`
		validOutput := validStructuredOutputJSON

		inner := newMultiOutputAgent(t, []string{invalidOutput, validOutput}, &callCount, nil)
		wrapped, err := NewAgent(inner, WithOutputValidationRetries(1))
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
		if runErr != nil {
			t.Fatalf("expected success after retry, got error: %v", runErr)
		}
		if atomic.LoadInt32(&callCount) != 2 {
			t.Errorf("expected 2 calls (initial + retry), got %d", callCount)
		}
	})

	t.Run("invalid_output_all_attempts_exhausted", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		invalidOutput := `{"invalid":"response"}`

		inner := newMultiOutputAgent(t, []string{invalidOutput, invalidOutput}, &callCount, nil)
		wrapped, err := NewAgent(inner, WithOutputValidationRetries(1))
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
		if runErr == nil {
			t.Fatal("expected error after exhausted retries")
		}
		if !errors.Is(runErr, ErrStructuredOutputSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredOutputSchemaValidation, got %v", runErr)
		}
		if !errors.Is(runErr, ErrStructuredIOSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredIOSchemaValidation (umbrella), got %v", runErr)
		}
		if atomic.LoadInt32(&callCount) != 2 {
			t.Errorf("expected 2 calls (initial + 1 retry), got %d", callCount)
		}
	})

	t.Run("extraction_error_then_valid_retries", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		invalidOutput := "analysis\n" + validStructuredOutputJSON + "\n```\nextra"
		validOutput := validStructuredOutputJSON

		inner := newMultiOutputAgent(t, []string{invalidOutput, validOutput}, &callCount, nil)
		wrapped, err := NewAgent(inner, WithOutputValidationRetries(1))
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
		if runErr != nil {
			t.Fatalf("expected success after retry, got error: %v", runErr)
		}
		if atomic.LoadInt32(&callCount) != 2 {
			t.Errorf("expected 2 calls (initial + retry), got %d", callCount)
		}
	})

	t.Run("extraction_error_all_attempts_exhausted", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		invalidOutput := "analysis\n" + validStructuredOutputJSON + "\n```\nextra"

		inner := newMultiOutputAgent(t, []string{invalidOutput, invalidOutput}, &callCount, nil)
		wrapped, err := NewAgent(inner, WithOutputValidationRetries(1))
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
		if runErr == nil {
			t.Fatal("expected error after exhausted retries")
		}
		if !errors.Is(runErr, ErrStructuredOutputSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredOutputSchemaValidation, got %v", runErr)
		}
		if !errors.Is(runErr, ErrStructuredIOSchemaValidation) {
			t.Fatalf("error should satisfy ErrStructuredIOSchemaValidation (umbrella), got %v", runErr)
		}
		if atomic.LoadInt32(&callCount) != 2 {
			t.Errorf("expected 2 calls (initial + 1 retry), got %d", callCount)
		}
	})

	t.Run("non_schema_errors_no_schema_sentinel", func(t *testing.T) {
		t.Parallel()

		var callCount int32
		executionErr := errors.New("execution error")

		inner := newMultiOutputAgent(t, []string{validStructuredOutputJSON}, &callCount, executionErr)
		wrapped, err := NewAgent(inner)
		if err != nil {
			t.Fatalf("NewAgent() error = %v", err)
		}

		_, runErr := runSingleTurn(t, wrapped, `{"input":"hello"}`)
		if runErr == nil {
			t.Fatal("expected error from inner agent")
		}
		if errors.Is(runErr, ErrStructuredInputSchemaValidation) {
			t.Fatal("non-schema error should not satisfy ErrStructuredInputSchemaValidation")
		}
		if errors.Is(runErr, ErrStructuredOutputSchemaValidation) {
			t.Fatal("non-schema error should not satisfy ErrStructuredOutputSchemaValidation")
		}
		if errors.Is(runErr, ErrStructuredIOSchemaValidation) {
			t.Fatal("non-schema error should not satisfy ErrStructuredIOSchemaValidation")
		}
	})
}
