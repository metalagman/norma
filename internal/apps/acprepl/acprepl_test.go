package acprepl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

var ansiSequenceRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestExtractACPToolEventPartText(t *testing.T) {
	tests := []struct {
		name     string
		part     *genai.Part
		expected string
	}{
		{
			name:     "nil part",
			part:     nil,
			expected: "",
		},
		{
			name:     "empty text",
			part:     &genai.Part{},
			expected: "",
		},
		{
			name:     "text content",
			part:     &genai.Part{Text: "Hello, World!"},
			expected: "Hello, World!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractACPToolEventPartText(tt.part)
			if got != tt.expected {
				t.Fatalf("extractACPToolEventPartText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestACPThoughtOutput(t *testing.T) {
	part := &genai.Part{
		Text:    "Let me think about this problem step by step...",
		Thought: true,
	}

	text := extractACPToolEventPartText(part)
	if text != "Let me think about this problem step by step..." {
		t.Fatalf("text = %q, want %q", text, "Let me think about this problem step by step...")
	}
	if !part.Thought {
		t.Fatal("part.Thought = false, want true")
	}
}

func TestACPFunctionCallDetection(t *testing.T) {
	part := &genai.Part{
		FunctionCall: &genai.FunctionCall{
			ID:   "call-123",
			Name: acpToolCallEventName,
			Args: map[string]any{"prompt": "hello"},
		},
	}

	text := extractACPToolEventPartText(part)
	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
	if part.FunctionCall == nil {
		t.Fatal("FunctionCall is nil, want non-nil")
	}
	if part.FunctionCall.ID != "call-123" {
		t.Fatalf("FunctionCall.ID = %q, want %q", part.FunctionCall.ID, "call-123")
	}
	if part.FunctionCall.Name != acpToolCallEventName {
		t.Fatalf("FunctionCall.Name = %q, want %q", part.FunctionCall.Name, acpToolCallEventName)
	}
}

func TestACPFunctionResponseDetection(t *testing.T) {
	part := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call-123",
			Name:     "acp_tool_call_update",
			Response: map[string]any{"output": "response content"},
		},
	}

	text := extractACPToolEventPartText(part)
	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
	if part.FunctionResponse == nil {
		t.Fatal("FunctionResponse is nil, want non-nil")
	}
	if part.FunctionResponse.ID != "call-123" {
		t.Fatalf("FunctionResponse.ID = %q, want %q", part.FunctionResponse.ID, "call-123")
	}
	if part.FunctionResponse.Name != "acp_tool_call_update" {
		t.Fatalf("FunctionResponse.Name = %q, want %q", part.FunctionResponse.Name, "acp_tool_call_update")
	}
}

func TestRenderACPToolEvent_AccumulatesThoughtChunksUntilText(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: "The user"},
	}))
	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: ` said "hello"`},
	}))
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{Text: "Hello"},
		{Text: "! How can I help you today?"},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	got := stdout.String()
	if !strings.Contains(got, "Thought: "+ansiGray+"The user said \"hello\""+ansiReset+"\n") {
		t.Fatalf("stdout = %q, want colored thought output", got)
	}
	wantPlain := "Thought: The user said \"hello\"\n\nHello! How can I help you today?\n"
	if gotPlain := stripANSI(got); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func TestRenderACPToolEvent_AccumulatesTextWithoutNewlineUntilFlush(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Text: "Hel"},
		{Text: "lo"},
	}))
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: acpToolCallEventName,
				Args: map[string]any{
					"title": "run shell",
					"rawInput": map[string]any{
						"command": "date",
					},
				},
			},
		},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	wantPlain := "Hello\nToolCall: run shell\n"
	if gotPlain := stripANSI(stdout.String()); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func TestRenderACPToolEvent_FlushesThoughtAtTurnComplete(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: "thinking..."},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	want := "Thought: " + ansiGray + "thinking..." + ansiReset + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRenderACPToolEvent_NormalizesThoughtWhitespace(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: "Line one.\n\n   Line two.\n"},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	want := "Thought: " + ansiGray + "Line one. Line two." + ansiReset + "\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRenderACPToolEvent_RendersMarkdownOnFlush(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Text: "**Hello** _world_"},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	gotPlain := stripANSI(stdout.String())
	if strings.Contains(gotPlain, "**Hello**") {
		t.Fatalf("plain stdout = %q, markdown markers should be rendered", gotPlain)
	}
	if !strings.Contains(strings.ToLower(gotPlain), "hello") {
		t.Fatalf("plain stdout = %q, should contain rendered text", gotPlain)
	}
}

func TestRenderACPToolEvent_NormalizesLeadingBlankLineInPlainText(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Text: "\n  Based on the environment info."},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	wantPlain := "Based on the environment info.\n"
	if gotPlain := stripANSI(stdout.String()); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func TestRenderACPToolEvent_IgnoresToolCallUpdateOutput(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	updatePart := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:   "call-dup",
			Name: "acp_tool_call_update",
			Response: map[string]any{
				"title":  "run shell",
				"status": "completed",
			},
		},
	}
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{updatePart}))
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{updatePart}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	wantPlain := ""
	if gotPlain := stripANSI(stdout.String()); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func TestRenderACPToolEvent_ThoughtToolThoughtThenSingleBlankBeforeUserText(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: "Need to call a tool."},
	}))
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-abc",
				Name: acpToolCallEventName,
				Args: map[string]any{
					"title": "bash",
				},
			},
		},
	}))
	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Thought: true, Text: "Got result."},
	}))
	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{Text: "It is 08:59:51."},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	wantPlain := "Thought: Need to call a tool.\nToolCall: bash\nThought: Got result.\n\nIt is 08:59:51.\n"
	if gotPlain := stripANSI(stdout.String()); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func TestRenderACPToolEvent_SuppressesDuplicateFinalTurnText(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(true, false, []*genai.Part{
		{Text: "hello"},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, []*genai.Part{
		{Text: "hello"},
	}))

	wantPlain := "hello\n"
	if gotPlain := stripANSI(stdout.String()); gotPlain != wantPlain {
		t.Fatalf("plain stdout = %q, want %q", gotPlain, wantPlain)
	}
}

func testEvent(partial bool, turnComplete bool, parts []*genai.Part) *session.Event {
	ev := session.NewEvent(context.Background(), "inv-1")
	if len(parts) > 0 {
		ev.Content = genai.NewContentFromParts(parts, genai.RoleModel)
	}
	ev.Partial = partial
	ev.TurnComplete = turnComplete
	return ev
}

func stripANSI(s string) string {
	return ansiSequenceRE.ReplaceAllString(s, "")
}

func TestRenderACPToolEvent_ToolCallHidesParams(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: acpToolCallEventName,
				Args: map[string]any{
					"title": "run shell",
					"rawInput": map[string]any{
						"command": "echo secret_password_123",
						"timeout": 30,
					},
				},
			},
		},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	output := stdout.String()
	gotPlain := stripANSI(output)

	// Verify params are NOT in output
	if strings.Contains(gotPlain, "secret_password_123") {
		t.Fatalf("plain stdout contains secret param: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "command") {
		t.Fatalf("plain stdout contains 'command' param key: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "timeout") {
		t.Fatalf("plain stdout contains 'timeout' param key: %q", gotPlain)
	}

	// Verify ToolCall params are intentionally hidden in REPL output
	// to keep transcripts readable and secure. Tool name remains visible.
	if !strings.Contains(gotPlain, "ToolCall: run shell") {
		t.Fatalf("plain stdout should contain 'ToolCall: run shell', got %q", gotPlain)
	}
}

func TestRenderACPToolEvent_ComplexParamsAreHidden(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-2",
				Name: acpToolCallEventName,
				Args: map[string]any{
					"title": "deep_parser",
					"rawInput": map[string]any{
						"structure": map[string]any{
							"level1": map[string]any{
								"secret": "hidden_value",
								"array":  []string{"item1", "item2"},
							},
						},
					},
				},
			},
		},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	output := stdout.String()
	gotPlain := stripANSI(output)

	// Verify no param serialization is visible
	if strings.Contains(gotPlain, "hidden_value") {
		t.Fatalf("plain stdout contains nested secret param: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "item1") || strings.Contains(gotPlain, "item2") {
		t.Fatalf("plain stdout contains array params: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "structure") || strings.Contains(gotPlain, "level1") {
		t.Fatalf("plain stdout contains param keys: %q", gotPlain)
	}

	// Verify only tool name is shown
	if !strings.Contains(gotPlain, "ToolCall: deep_parser") {
		t.Fatalf("plain stdout should contain 'ToolCall: deep_parser', got %q", gotPlain)
	}
}

func TestRenderACPToolEvent_ToolNameStillVisible(t *testing.T) {
	testCases := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "simple tool name",
			title:    "bash",
			expected: "ToolCall: bash",
		},
		{
			name:     "tool name with spaces",
			title:    "run shell script",
			expected: "ToolCall: run shell script",
		},
		{
			name:     "tool name with special chars",
			title:    "grep-find_files.sh",
			expected: "ToolCall: grep-find_files.sh",
		},
		{
			name:     "empty title uses default",
			title:    "",
			expected: "ToolCall: " + acpToolCallEventName,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
			accumulator := newACPToolTurnAccumulator(ui)

			renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
				{
					FunctionCall: &genai.FunctionCall{
						ID:   "call-test",
						Name: acpToolCallEventName,
						Args: map[string]any{
							"title": tc.title,
							"rawInput": map[string]any{
								"param1": "value1",
								"param2": "value2",
							},
						},
					},
				},
			}))
			renderACPToolEvent(accumulator, testEvent(false, true, nil))

			output := stdout.String()
			gotPlain := stripANSI(output)

			if !strings.Contains(gotPlain, tc.expected) {
				t.Fatalf("plain stdout should contain %q, got %q", tc.expected, gotPlain)
			}

			// Verify params are hidden
			if strings.Contains(gotPlain, "param1") || strings.Contains(gotPlain, "value1") {
				t.Fatalf("plain stdout should not contain params: %q", gotPlain)
			}
		})
	}
}

func TestRenderACPToolEvent_LargeToolCallPayload(t *testing.T) {
	var stdout bytes.Buffer
	ui := newACPToolTerminal(strings.NewReader(""), &stdout, &stdout)
	accumulator := newACPToolTurnAccumulator(ui)

	// Create a large nested parameter payload to test payload hiding
	largePayload := make(map[string]any)
	for i := 0; i < 100; i++ {
		nestedMap := make(map[string]any)
		for j := 0; j < 10; j++ {
			nestedMap[fmt.Sprintf("key_%d_%d", i, j)] = fmt.Sprintf("value_%d_%d", i, j)
		}
		largePayload[fmt.Sprintf("param_%d", i)] = nestedMap
	}

	renderACPToolEvent(accumulator, testEvent(false, false, []*genai.Part{
		{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-large",
				Name: acpToolCallEventName,
				Args: map[string]any{
					"title":    "large_payload_tool",
					"rawInput": largePayload,
				},
			},
		},
	}))
	renderACPToolEvent(accumulator, testEvent(false, true, nil))

	output := stdout.String()
	gotPlain := stripANSI(output)

	// Verify tool name is visible
	if !strings.Contains(gotPlain, "ToolCall: large_payload_tool") {
		t.Fatalf("plain stdout should contain 'ToolCall: large_payload_tool', got %q", gotPlain)
	}

	// Verify large payload content is NOT rendered
	if strings.Contains(gotPlain, "param_") {
		t.Fatalf("plain stdout should not contain payload param names: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "key_") {
		t.Fatalf("plain stdout should not contain nested param keys: %q", gotPlain)
	}
	if strings.Contains(gotPlain, "value_") {
		t.Fatalf("plain stdout should not contain payload values: %q", gotPlain)
	}
}

func TestRunREPLRejectsNilStreams(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()
	command := []string{"fake", "command"}

	testCases := []struct {
		name   string
		stdin  io.Reader
		stdout io.Writer
		stderr io.Writer
		want   string
	}{
		{
			name:   "nil stdin",
			stdin:  nil,
			stdout: io.Discard,
			stderr: io.Discard,
			want:   "stdin is required",
		},
		{
			name:   "nil stdout",
			stdin:  strings.NewReader(""),
			stdout: nil,
			stderr: io.Discard,
			want:   "stdout is required",
		},
		{
			name:   "nil stderr",
			stdin:  strings.NewReader(""),
			stdout: io.Discard,
			stderr: nil,
			want:   "stderr is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := RunREPL(ctx, workingDir, command, "", "", tc.stdin, tc.stdout, tc.stderr)
			if err == nil {
				t.Fatal("RunREPL() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunREPL() error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}
