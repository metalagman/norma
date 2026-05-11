package goalkeeper

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestBuildCodexACPCommand(t *testing.T) {
	t.Parallel()

	got := BuildCodexACPCommand("")
	want := []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildCodexACPCommand(\"\") = %v, want %v", got, want)
	}

	got = BuildCodexACPCommand("/tmp/bridge")
	if len(got) != 1 || got[0] != "/tmp/bridge" {
		t.Fatalf("BuildCodexACPCommand(custom) = %v, want custom binary only", got)
	}
}

func TestInstructionsDefineWorkerAndValidator(t *testing.T) {
	t.Parallel()

	worker := workerInstruction()
	for _, want := range []string{
		"Goalkeeper worker agent",
		"Do the requested work in the current working directory.",
		"Return a concise plain-text summary",
	} {
		if !strings.Contains(worker, want) {
			t.Fatalf("workerInstruction() = %q, want substring %q", worker, want)
		}
	}

	validator := validatorInstruction()
	for _, want := range []string{
		"Goalkeeper validator agent",
		"shared ADK session context",
		"Do not intentionally mutate files",
		"`verdict: pass` or `verdict: fail`",
	} {
		if !strings.Contains(validator, want) {
			t.Fatalf("validatorInstruction() = %q, want substring %q", validator, want)
		}
	}
}

func TestWorkflowRunsWorkerThenValidatorWithSharedSession(t *testing.T) {
	t.Parallel()

	var order []string
	worker := mustNewTestAgent(t, workerAgentName, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			order = append(order, workerAgentID)
			if err := ctx.Session().State().Set("worker_result", "created artifact"); err != nil {
				yield(nil, err)
				return
			}
			yield(textEvent(ctx.InvocationID(), "worker result"), nil)
		}
	})
	validator := mustNewTestAgent(t, validatorAgentName, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			order = append(order, validatorAgentID)
			value, err := ctx.Session().State().Get("worker_result")
			if err != nil {
				yield(nil, err)
				return
			}
			yield(textEvent(ctx.InvocationID(), "verdict: pass\nworker_result="+value.(string)), nil)
		}
	})

	workflow, err := newWorkflowAgent(worker, validator, zerolog.Nop())
	if err != nil {
		t.Fatalf("newWorkflowAgent() error = %v", err)
	}
	got := runTestAgentOnce(t, workflow, "Goal:\nship")
	if got != "verdict: pass\nworker_result=created artifact" {
		t.Fatalf("workflow output = %q, want validator output from shared state", got)
	}
	if strings.Join(order, ",") != "worker,validator" {
		t.Fatalf("order = %v, want worker then validator", order)
	}
}

func TestWorkflowLogsStepLifecycle(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	worker := mustNewTestAgent(t, workerAgentName, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			yield(textEvent(ctx.InvocationID(), "worker result"), nil)
		}
	})
	validator := mustNewTestAgent(t, validatorAgentName, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			yield(textEvent(ctx.InvocationID(), "verdict: pass"), nil)
		}
	})

	workflow, err := newWorkflowAgent(worker, validator, logger)
	if err != nil {
		t.Fatalf("newWorkflowAgent() error = %v", err)
	}
	_ = runTestAgentOnce(t, workflow, "Goal:\nship")

	events := decodeLogEvents(t, logs.String())
	wantMessages := []string{
		"goalkeeper step started",
		"goalkeeper step completed",
		"goalkeeper step started",
		"goalkeeper step completed",
	}
	if len(events) != len(wantMessages) {
		t.Fatalf("log event count = %d, want %d; logs=%s", len(events), len(wantMessages), logs.String())
	}
	for i, want := range wantMessages {
		if events[i]["message"] != want {
			t.Fatalf("event %d message = %v, want %q", i, events[i]["message"], want)
		}
	}
	if events[0]["step"] != workerAgentID || events[0]["step_index"].(float64) != 1 {
		t.Fatalf("worker start event = %#v, want worker step 1", events[0])
	}
	if events[2]["step"] != validatorAgentID || events[2]["step_index"].(float64) != 2 {
		t.Fatalf("validator start event = %#v, want validator step 2", events[2])
	}
	if events[3]["event_count"].(float64) != 1 || events[3]["response_len"].(float64) == 0 {
		t.Fatalf("validator completion event = %#v, want event count and response length", events[3])
	}
}

func TestRunPrintsValidatorResultAndClosesAgents(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var configs []acpRuntimeConfig
	var agents []*testClosableAgent

	err := run(context.Background(), Config{
		Goal:       "count lines",
		WorkingDir: t.TempDir(),
		BridgeBin:  "/tmp/bridge",
		Stdout:     &stdout,
		Stderr:     &stderr,
		Logger:     ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			configs = append(configs, cfg)
			ag := &testClosableAgent{
				Agent: mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {}
				}),
			}
			agents = append(agents, ag)
			return ag, nil
		},
		runAgent: func(_ context.Context, a agent.Agent, prompt string, onOutput func(string)) (string, error) {
			if prompt != "Goal:\ncount lines" {
				t.Fatalf("prompt = %q, want formatted goal", prompt)
			}
			if len(a.SubAgents()) != 2 {
				t.Fatalf("workflow subagents = %d, want 2", len(a.SubAgents()))
			}
			onOutput("verdict: pass")
			return "verdict: pass\nall good", nil
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("created agents = %d, want 2", len(configs))
	}
	if configs[0].AgentID != workerAgentID || configs[1].AgentID != validatorAgentID {
		t.Fatalf("agent IDs = %s,%s; want worker,validator", configs[0].AgentID, configs[1].AgentID)
	}
	if got := strings.Join(configs[0].Command, " "); got != "/tmp/bridge" {
		t.Fatalf("worker command = %q, want custom bridge", got)
	}
	if !strings.Contains(configs[1].Instruction, "shared ADK session context") {
		t.Fatalf("validator instruction = %q, want shared session guidance", configs[1].Instruction)
	}
	for _, ag := range agents {
		if !ag.closed {
			t.Fatalf("agent %s was not closed", ag.Name())
		}
	}
	got := stdout.String()
	if !strings.Contains(got, "verdict: pass\nall good\n") {
		t.Fatalf("stdout = %q, want validator result", got)
	}
	if !strings.Contains(got, "Total run time: ") {
		t.Fatalf("stdout = %q, want total run time", got)
	}
}

func TestRunLogsFinalVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		result          string
		wantVerdict     string
		wantGoalReached bool
	}{
		{
			name:            "pass",
			result:          "verdict: pass\nall good",
			wantVerdict:     "pass",
			wantGoalReached: true,
		},
		{
			name:            "fail",
			result:          "verdict: fail\nmissing tests",
			wantVerdict:     "fail",
			wantGoalReached: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			logger := zerolog.New(&logs)
			err := run(context.Background(), Config{
				Goal:   "ship",
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
				Logger: &logger,
			}, runtimeDeps{
				newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
					return &testClosableAgent{Agent: mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
						return func(yield func(*session.Event, error) bool) {}
					})}, nil
				},
				runAgent: func(context.Context, agent.Agent, string, func(string)) (string, error) {
					return tc.result, nil
				},
			})
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}

			events := decodeLogEvents(t, logs.String())
			event := findLogEvent(events, "goalkeeper validation completed")
			if event == nil {
				t.Fatalf("missing validation log event; logs=%s", logs.String())
			}
			if event["verdict"] != tc.wantVerdict || event["goal_reached"] != tc.wantGoalReached {
				t.Fatalf("validation event = %#v, want verdict=%q goal_reached=%t", event, tc.wantVerdict, tc.wantGoalReached)
			}
		})
	}
}

func TestRunRejectsEmptyGoal(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), Config{Goal: "  "}, runtimeDeps{})
	if err == nil || err.Error() != "goal is required" {
		t.Fatalf("run(empty goal) error = %v, want goal required", err)
	}
}

func TestParseVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		output          string
		wantVerdict     string
		wantGoalReached bool
	}{
		{
			name:            "pass",
			output:          "verdict: pass\nall good",
			wantVerdict:     "pass",
			wantGoalReached: true,
		},
		{
			name:            "fail",
			output:          "verdict: fail\nmissing tests",
			wantVerdict:     "fail",
			wantGoalReached: false,
		},
		{
			name:            "missing",
			output:          "all good",
			wantVerdict:     "unknown",
			wantGoalReached: false,
		},
		{
			name:            "malformed",
			output:          "verdict pass",
			wantVerdict:     "unknown",
			wantGoalReached: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotVerdict, gotGoalReached := parseVerdict(tc.output)
			if gotVerdict != tc.wantVerdict || gotGoalReached != tc.wantGoalReached {
				t.Fatalf("parseVerdict(%q) = %q, %t; want %q, %t", tc.output, gotVerdict, gotGoalReached, tc.wantVerdict, tc.wantGoalReached)
			}
		})
	}
}

type testClosableAgent struct {
	agent.Agent
	closed bool
}

func (a *testClosableAgent) Close() error {
	a.closed = true
	return nil
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

func ptrLogger(l zerolog.Logger) *zerolog.Logger {
	return &l
}

func decodeLogEvents(t *testing.T, logs string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("json.Unmarshal(%q) error = %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func findLogEvent(events []map[string]any, message string) map[string]any {
	for _, event := range events {
		if event["message"] == message {
			return event
		}
	}
	return nil
}
