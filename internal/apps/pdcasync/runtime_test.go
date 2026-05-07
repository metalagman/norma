package pdcasync

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

const statusError = "error"

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

func TestRootInstructionDefinesSyncCoordinator(t *testing.T) {
	t.Parallel()

	got := rootInstruction(7)
	for _, want := range []string{
		"synchronous PDCA playground",
		"pdca.prompt_subagent",
		"The canonical PDCA check verdict literals are `pass` and `fail`.",
		"The canonical PDCA act decision literals are `close`, `continue`, and `replan`.",
		"The legal verdict/decision pairs are `pass + close`, `fail + continue`, and `fail + replan`.",
		"The invalid verdict/decision pairs are `pass + continue`, `pass + replan`, and `fail + close`.",
		"Any `rollback` decision is invalid PDCA output.",
		"`close` means the work is done, `continue` means run another PDCA iteration, and `replan` means the work is not ready to close and needs replanning.",
		"Child invocation is a blackbox runtime action.",
		"There is no task, envelope, queue, report, or finish protocol",
		"The runtime does not enforce phase order.",
		"When you prompt plan after an act call, that starts the next iteration.",
		"The run is limited to 7 iterations.",
		"return your final plain-text answer directly",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"schedule_task",
		"finish the run",
		"report_to",
		"plan -> do -> check -> act",
		"fixed phase order",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestChildAgentInstructionsStayPlainText(t *testing.T) {
	t.Parallel()

	for agentID, instruction := range childAgentInstructions {
		lower := strings.ToLower(instruction)
		if !strings.Contains(lower, "plain-text") && !strings.Contains(lower, "plain text") {
			t.Fatalf("%s instruction = %q, want plain-text guidance", agentID, instruction)
		}
		if !strings.Contains(instruction, "Do not use JSON, schemas, field names, or code fences.") {
			t.Fatalf("%s instruction = %q, want plain-text safety guidance", agentID, instruction)
		}
	}

	checkInstruction := childAgentInstructions["check"]
	for _, want := range []string{
		"Your semantic output is the PDCA verdict for the current iteration: pass or fail.",
		"`verdict: pass` or `verdict: fail`",
	} {
		if !strings.Contains(checkInstruction, want) {
			t.Fatalf("check instruction = %q, want substring %q", checkInstruction, want)
		}
	}

	actInstruction := childAgentInstructions["act"]
	for _, want := range []string{
		"Your semantic output is the PDCA decision for the current iteration: close, continue, or replan.",
		"If the verdict is pass, the only legal decision is close.",
		"If the verdict is fail, the legal decisions are continue or replan.",
		"Never return rollback.",
		"`decision: close`, `decision: continue`, or `decision: replan`",
	} {
		if !strings.Contains(actInstruction, want) {
			t.Fatalf("act instruction = %q, want substring %q", actInstruction, want)
		}
	}
}

func TestPromptSubagentReturnsRawChildOutput(t *testing.T) {
	t.Parallel()

	svc := newService(zerolog.Nop(), 3)
	svc.agents["plan"] = fakeRunner{result: "plain plan"}

	res, out, err := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "plan",
		Prompt:    "count lines of go code",
	})
	if err != nil {
		t.Fatalf("promptSubagent() error = %v", err)
	}
	if out.Status != "ok" || out.Result != "plain plan" || out.Iteration != 1 {
		t.Fatalf("promptSubagent() output = %+v", out)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("promptSubagent() result = %#v, want one text content item", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "plain plan" {
		t.Fatalf("tool text = %#v, want raw child output", res.Content[0])
	}
}

func TestPromptSubagentUnknownAgentRejected(t *testing.T) {
	t.Parallel()

	svc := newService(zerolog.Nop(), 2)
	_, out, err := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "unknown",
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("promptSubagent() error = %v", err)
	}
	if out.Status != statusError || !strings.Contains(out.Message, `unknown agent_name "unknown"`) {
		t.Fatalf("promptSubagent() output = %+v", out)
	}
}

func TestPlanAfterActStartsNextIteration(t *testing.T) {
	t.Parallel()

	svc := newService(zerolog.Nop(), 2)
	svc.agents["act"] = fakeRunner{result: "decision: replan"}
	svc.agents["plan"] = fakeRunner{result: "new plan"}

	_, out, _ := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "act",
		Prompt:    "check output",
	})
	if out.Iteration != 1 {
		t.Fatalf("act iteration = %d, want 1", out.Iteration)
	}
	_, out, _ = svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "plan",
		Prompt:    "goal",
	})
	if out.Iteration != 2 {
		t.Fatalf("plan iteration = %d, want 2 after act->plan loopback", out.Iteration)
	}
}

func TestPlanAfterActRespectsMaxIterations(t *testing.T) {
	t.Parallel()

	svc := newService(zerolog.Nop(), 1)
	svc.agents["act"] = fakeRunner{result: "decision: replan"}
	svc.agents["plan"] = fakeRunner{result: "new plan"}

	if _, _, err := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "act",
		Prompt:    "check output",
	}); err != nil {
		t.Fatalf("act promptSubagent() error = %v", err)
	}
	_, out, err := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "plan",
		Prompt:    "goal",
	})
	if err != nil {
		t.Fatalf("promptSubagent() error = %v", err)
	}
	if out.Status != statusError || !strings.Contains(out.Message, "max_iterations 1 exceeded") {
		t.Fatalf("promptSubagent() output = %+v", out)
	}
}

func TestRunPrintsCoordinatorResult(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	root := &fakeClosableRunner{result: "final answer"}
	children := &fakeRunnerSet{runners: map[string]childInvoker{
		"plan":  fakeRunner{result: "plan"},
		"do":    fakeRunner{result: "do"},
		"check": fakeRunner{result: "check"},
		"act":   fakeRunner{result: "act"},
	}}
	err := run(context.Background(), Config{
		Goal:          "count lines",
		WorkingDir:    t.TempDir(),
		MaxIterations: 3,
		Stdout:        &stdout,
		Stderr:        &bytes.Buffer{},
		Logger:        ptrLogger(zerolog.Nop()),
	}, runtimeDeps{
		newRootSession: func(context.Context, acpSessionConfig) (closableRunner, error) { return root, nil },
		newChildSet:    func(context.Context, childSessionSetConfig) (runnerSet, error) { return children, nil },
		startServer: func(context.Context, *service, string) (*httpServerResult, error) {
			return &httpServerResult{Addr: "127.0.0.1:1", Close: func() error { return nil }}, nil
		},
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "final answer\n") {
		t.Fatalf("stdout = %q, want final answer line", got)
	}
	if !strings.Contains(got, "Total run time: ") {
		t.Fatalf("stdout = %q, want total run time line", got)
	}
	if !root.closed {
		t.Fatal("root runner was not closed")
	}
	if !children.closed {
		t.Fatal("child runners were not closed")
	}
}

type fakeRunner struct {
	result string
	err    error
}

func (r fakeRunner) RunTask(context.Context, string, string) (string, error) {
	return r.result, r.err
}

type fakeClosableRunner struct {
	result string
	err    error
	closed bool
}

func (r *fakeClosableRunner) RunTask(context.Context, string, string) (string, error) {
	return r.result, r.err
}

func (r *fakeClosableRunner) Close() error {
	r.closed = true
	return nil
}

type fakeRunnerSet struct {
	runners map[string]childInvoker
	closed  bool
}

func (s *fakeRunnerSet) Runner(agentID string) childInvoker {
	return s.runners[agentID]
}

func (s *fakeRunnerSet) Close() error {
	s.closed = true
	return nil
}

func ptrLogger(l zerolog.Logger) *zerolog.Logger {
	return &l
}

func TestPromptSubagentChildFailurePropagatesAsToolError(t *testing.T) {
	t.Parallel()

	svc := newService(zerolog.Nop(), 2)
	svc.agents["check"] = fakeRunner{err: errors.New("boom")}

	_, out, err := svc.promptSubagent(context.Background(), nil, promptSubagentInput{
		AgentName: "check",
		Prompt:    "verify",
	})
	if err != nil {
		t.Fatalf("promptSubagent() error = %v", err)
	}
	if out.Status != statusError || !strings.Contains(out.Message, `child agent "check" failed: boom`) {
		t.Fatalf("promptSubagent() output = %+v", out)
	}
}
