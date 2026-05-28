package goalkeeperactor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
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

func TestRunPrintsResultAndClosesAgents(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var configs []acpRuntimeConfig
	var agents []*testClosableAgent

	workerCalls := int32(0)
	validatorCalls := int32(0)
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
			var run func(agent.InvocationContext) iter.Seq2[*session.Event, error]
			switch cfg.AgentID {
			case workerAgentID:
				run = func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						atomic.AddInt32(&workerCalls, 1)
						yield(textEvent(ctx.InvocationID(), "worker summary"), nil)
					}
				}
			case validatorAgentID:
				run = func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						atomic.AddInt32(&validatorCalls, 1)
						yield(textEvent(ctx.InvocationID(), "verdict: pass\nall good"), nil)
					}
				}
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
			ag := &testClosableAgent{Agent: mustNewTestAgent(t, cfg.Name, run)}
			agents = append(agents, ag)
			return ag, nil
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := atomic.LoadInt32(&workerCalls); got != 1 {
		t.Fatalf("worker calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&validatorCalls); got != 1 {
		t.Fatalf("validator calls = %d, want 1", got)
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

func TestRunRetriesUntilPass(t *testing.T) {
	t.Parallel()

	var workerCalls atomic.Int32
	var validatorCalls atomic.Int32

	err := run(context.Background(), Config{
		Goal:          "ship",
		WorkingDir:    t.TempDir(),
		MaxIterations: 5,
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						workerCalls.Add(1)
						yield(textEvent(ctx.InvocationID(), "worker result"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						count := validatorCalls.Add(1)
						if count == 1 {
							yield(textEvent(ctx.InvocationID(), "verdict: fail\nfirst try"), nil)
							return
						}
						yield(textEvent(ctx.InvocationID(), "verdict: pass\nsecond try"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if got := workerCalls.Load(); got != 2 {
		t.Fatalf("worker calls = %d, want 2", got)
	}
	if got := validatorCalls.Load(); got != 2 {
		t.Fatalf("validator calls = %d, want 2", got)
	}
}

func TestRunWorkerPromptIncludesRawGoal(t *testing.T) {
	t.Parallel()

	const goal = "count lines of go code"
	var seenWorkerPrompt string

	err := run(context.Background(), Config{
		Goal:          goal,
		WorkingDir:    t.TempDir(),
		MaxIterations: 1,
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						seenWorkerPrompt = userPromptText(ctx.UserContent())
						yield(textEvent(ctx.InvocationID(), "worker done"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(textEvent(ctx.InvocationID(), "verdict: pass\nvalidated"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if seenWorkerPrompt != goal {
		t.Fatalf("worker prompt = %q, want raw goal %q", seenWorkerPrompt, goal)
	}
	if strings.Contains(seenWorkerPrompt, "Goal:") {
		t.Fatalf("worker prompt = %q, want no Goal: wrapper", seenWorkerPrompt)
	}
}

func TestRunUsesConversationScopedSessionPerActor(t *testing.T) {
	t.Parallel()

	var workerSessionID string
	var validatorSessionID string

	err := run(context.Background(), Config{
		Goal:          "ship",
		WorkingDir:    t.TempDir(),
		MaxIterations: 1,
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						workerSessionID = ctx.Session().ID()
						yield(textEvent(ctx.InvocationID(), "worker done"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						validatorSessionID = ctx.Session().ID()
						yield(textEvent(ctx.InvocationID(), "verdict: pass\nvalidated"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	workerBase, workerRole, ok := strings.Cut(workerSessionID, "::")
	if !ok {
		t.Fatalf("worker session id = %q, want conversation::actor format", workerSessionID)
	}
	validatorBase, validatorRole, ok := strings.Cut(validatorSessionID, "::")
	if !ok {
		t.Fatalf("validator session id = %q, want conversation::actor format", validatorSessionID)
	}
	if workerBase == "" || validatorBase == "" {
		t.Fatalf("session bases must be non-empty: worker=%q validator=%q", workerSessionID, validatorSessionID)
	}
	if workerBase != validatorBase {
		t.Fatalf("session bases differ: worker=%q validator=%q", workerBase, validatorBase)
	}
	if workerRole != workerActorID {
		t.Fatalf("worker role suffix = %q, want %q", workerRole, workerActorID)
	}
	if validatorRole != validatorActorID {
		t.Fatalf("validator role suffix = %q, want %q", validatorRole, validatorActorID)
	}
}

func TestRunValidatorPromptIncludesGoalAndWorkerOutput(t *testing.T) {
	t.Parallel()

	const goal = "count lines of go code"
	const workerResult = "Go files: 264\nTotal lines: 52099"

	var seenValidatorPrompt string
	err := run(context.Background(), Config{
		Goal:          goal,
		WorkingDir:    t.TempDir(),
		MaxIterations: 1,
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(textEvent(ctx.InvocationID(), workerResult), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						seenValidatorPrompt = userPromptText(ctx.UserContent())
						yield(textEvent(ctx.InvocationID(), "verdict: pass\nvalidated"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(seenValidatorPrompt, goal) {
		t.Fatalf("validator prompt = %q, want goal %q", seenValidatorPrompt, goal)
	}
	if !strings.Contains(seenValidatorPrompt, workerResult) {
		t.Fatalf("validator prompt = %q, want worker result %q", seenValidatorPrompt, workerResult)
	}
	if !strings.Contains(seenValidatorPrompt, "verdict: pass") || !strings.Contains(seenValidatorPrompt, "verdict: fail") {
		t.Fatalf("validator prompt = %q, want explicit verdict contract", seenValidatorPrompt)
	}
}

func TestRunErrorsWhenMaxIterationsExhausted(t *testing.T) {
	t.Parallel()

	assertRunFailsAfterMaxIterations(t,
		"verdict: fail\nmissing evidence",
		"goalkeeper validation did not pass",
		"verdict: fail\nmissing evidence\n",
	)
}

func TestRunTreatsMissingVerdictAsFail(t *testing.T) {
	t.Parallel()

	assertRunFailsAfterMaxIterations(t,
		"52099 lines of Go code.",
		"verdict=unknown",
		"52099 lines of Go code.\n",
	)
}

func TestRunPropagatesWorkerError(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), Config{
		Goal:       "ship",
		WorkingDir: t.TempDir(),
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
		Logger:     ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(nil, errors.New("worker boom"))
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(textEvent(ctx.InvocationID(), "verdict: pass"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err == nil {
		t.Fatal("run() error = nil, want worker error")
	}
	if !strings.Contains(err.Error(), "worker step failed") {
		t.Fatalf("run() error = %v, want worker step failed", err)
	}
}

func TestRunPropagatesValidatorError(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), Config{
		Goal:       "ship",
		WorkingDir: t.TempDir(),
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
		Logger:     ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(textEvent(ctx.InvocationID(), "worker summary"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						yield(nil, errors.New("validator boom"))
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err == nil {
		t.Fatal("run() error = nil, want validator error")
	}
	if !strings.Contains(err.Error(), "validator step failed") {
		t.Fatalf("run() error = %v, want validator step failed", err)
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

func assertRunFailsAfterMaxIterations(t *testing.T, validatorOutput, wantErrContains, wantStdoutContains string) {
	t.Helper()

	var workerCalls atomic.Int32
	var validatorCalls atomic.Int32
	var stdout bytes.Buffer

	err := run(context.Background(), Config{
		Goal:          "ship",
		WorkingDir:    t.TempDir(),
		MaxIterations: 2,
		Stdout:        &stdout,
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newACPAgent: func(_ context.Context, cfg acpRuntimeConfig) (closableAgent, error) {
			switch cfg.AgentID {
			case workerAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						workerCalls.Add(1)
						yield(textEvent(ctx.InvocationID(), "worker result"), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			case validatorAgentID:
				ag := mustNewTestAgent(t, cfg.Name, func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						validatorCalls.Add(1)
						yield(textEvent(ctx.InvocationID(), validatorOutput), nil)
					}
				})
				return &testClosableAgent{Agent: ag}, nil
			default:
				return nil, fmt.Errorf("unexpected agent id %q", cfg.AgentID)
			}
		},
	})
	if err == nil {
		t.Fatal("run() error = nil, want non-pass verdict error")
	}
	if !strings.Contains(err.Error(), wantErrContains) {
		t.Fatalf("run() error = %v, want substring %q", err, wantErrContains)
	}
	if got := workerCalls.Load(); got != 2 {
		t.Fatalf("worker calls = %d, want 2", got)
	}
	if got := validatorCalls.Load(); got != 2 {
		t.Fatalf("validator calls = %d, want 2", got)
	}
	if !strings.Contains(stdout.String(), wantStdoutContains) {
		t.Fatalf("stdout = %q, want substring %q", stdout.String(), wantStdoutContains)
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

func textEvent(invocationID string, text string) *session.Event {
	ev := session.NewEvent(invocationID)
	ev.Content = genai.NewContentFromText(text, genai.RoleModel)
	return ev
}

func userPromptText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part == nil || part.Thought || strings.TrimSpace(part.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(part.Text))
	}
	return strings.Join(parts, "\n\n")
}

func ptrLogger(l zerolog.Logger) *zerolog.Logger {
	return &l
}
