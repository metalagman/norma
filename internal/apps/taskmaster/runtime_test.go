package taskmaster

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
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

func TestFixedACPConfig(t *testing.T) {
	t.Parallel()

	if defaultAgentType != "codex_acp" {
		t.Fatalf("defaultAgentType = %q, want codex_acp", defaultAgentType)
	}
	if defaultModel != "gpt-5.3-codex" {
		t.Fatalf("defaultModel = %q, want gpt-5.3-codex", defaultModel)
	}
}

func TestRootInstructionDefinesStrictPDCA(t *testing.T) {
	t.Parallel()

	got := rootInstruction()
	for _, want := range []string{
		"strict PDCA workflow",
		"plan -> do -> check -> act",
		"Always start a new goal with plan.",
		"You receive only prompt text as your turn input.",
		"Runtime task routing and bookkeeping are internal",
		"do not author task-specific methodology, examples, commands, acceptance criteria, or execution instructions yourself",
		"The child agent's own system prompt defines how that phase works.",
		"For plan, pass only the raw goal text.",
		"For do, pass only the prior plan output.",
		"Goal:, Plan output:, Do output:.",
		"For act, pass only a neutral Check output: section.",
		"Child agents return freeform plain text, not structured role payloads.",
		"You interpret check and act outputs semantically from their plain text.",
		"helpful labels like `verdict:` or `decision:`",
		"If an act output clearly recommends replan, more planning is required before further execution.",
		"You decide the next child task yourself from the prompt text you receive.",
		"The report_to field means where task completion should be reported.",
		"Do not read files, execute scripts, or perform worker work yourself.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"`continue`",
		"call taskmaster.finish with a concise replan-required summary",
		"Create a concrete execution plan",
		"include exact command",
		"Return a concise plan suitable for immediate execution",
		"The check phase returns lowercase `pass` or `fail`",
		"The act phase returns lowercase `close` or `replan`",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestChildAgentInstructionsUseStrictPDCAContract(t *testing.T) {
	t.Parallel()

	checkInstruction := childAgentInstructions["check"]
	for _, want := range []string{
		"strict PDCA flow",
		"plain-text assessment",
		"`verdict: pass` or `verdict: fail`",
		"Do not use JSON, schemas, field names, or code fences.",
	} {
		if !strings.Contains(checkInstruction, want) {
			t.Fatalf("check instruction = %q, want substring %q", checkInstruction, want)
		}
	}
	for _, unwanted := range []string{
		"check_output",
		"acceptance_criteria",
		"do_steps",
		"Return lowercase `pass` only",
		"Otherwise return lowercase `fail`",
	} {
		if strings.Contains(checkInstruction, unwanted) {
			t.Fatalf("check instruction = %q, do not want substring %q", checkInstruction, unwanted)
		}
	}

	actInstruction := childAgentInstructions["act"]
	for _, want := range []string{
		"advisory input for the root taskmaster agent",
		"plain-text recommendation",
		"`decision: close` or `decision: replan`",
		"Do not use JSON, schemas, field names, or code fences.",
	} {
		if !strings.Contains(actInstruction, want) {
			t.Fatalf("act instruction = %q, want substring %q", actInstruction, want)
		}
	}
	for _, unwanted := range []string{
		"`continue`",
		"`rollback`",
		"act_output",
		"If the verdict is `pass`, return lowercase `close`.",
		"If the verdict is `fail`, return lowercase `replan`",
	} {
		if strings.Contains(actInstruction, unwanted) {
			t.Fatalf("act instruction = %q, do not want substring %q", actInstruction, unwanted)
		}
	}
}

func TestFormatInitialGoalTaskInputIncludesPDCAPolicy(t *testing.T) {
	t.Parallel()

	got := formatInitialGoalTaskInput("ship it")
	for _, want := range []string{
		"Goal:",
		"ship it",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatInitialGoalTaskInput() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"PDCA MODE:",
		"plan -> do -> check -> act",
		"`continue`",
		"call taskmaster.finish with a concise replan summary",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("formatInitialGoalTaskInput() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestFormatNotificationTaskInputIncludesPDCAContext(t *testing.T) {
	t.Parallel()

	got := formatNotificationTaskInput(&task{
		ID:       "task-plan",
		Locator:  newAgentLocator("plan"),
		ReportTo: newAgentLocator(taskmasterAgentID),
		Status:   taskStatusCompleted,
		Output:   "planned",
	})
	for _, want := range []string{
		"Task task-plan completed.",
		"Result:",
		"planned",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatNotificationTaskInput() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"TASK ENVELOPE:",
		`"phase":`,
		`"source_locator":`,
		`"report_to":`,
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("formatNotificationTaskInput() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestWriteRunOutputIncludesTotalRunTime(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := writeRunOutput(&stdout, "final answer", "1.234s"); err != nil {
		t.Fatalf("writeRunOutput() error = %v", err)
	}
	if got := stdout.String(); got != "final answer\nTotal run time: 1.234s\n" {
		t.Fatalf("stdout = %q, want summary plus total run time", got)
	}
}

func TestWriteRunOutputPrintsElapsedWithoutSummary(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := writeRunOutput(&stdout, "   ", "0s"); err != nil {
		t.Fatalf("writeRunOutput() error = %v", err)
	}
	if got := stdout.String(); got != "Total run time: 0s\n" {
		t.Fatalf("stdout = %q, want elapsed only", got)
	}
}

func TestScheduleTaskQueuesImmediately(t *testing.T) {
	t.Parallel()

	planRunner := &fakeTaskRunner{
		result:  "planned",
		started: make(chan string, 1),
		release: make(chan struct{}),
	}
	rootRunner := &fakeTaskRunner{started: make(chan string, 1)}
	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: rootRunner,
		plan:       planRunner,
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop())
	service.coordinator = coordinator
	started := make(chan string, 1)
	go func() {
		started <- <-planRunner.started
	}()

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:  "task-plan",
		Locator: newAgentLocator("plan"),
		Prompt:  "Plan the goal",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.Status != string(taskStatusQueued) {
		t.Fatalf("out.Status = %q, want queued", out.Status)
	}
	select {
	case got := <-started:
		if got != "Plan the goal" {
			t.Fatalf("worker prompt = %q, want raw prompt only", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker task was not started")
	}
	select {
	case prompt := <-rootRunner.started:
		t.Fatalf("taskmaster got prompt %q before worker finished, want async scheduling", prompt)
	default:
	}
	close(planRunner.release)
}

func TestScheduleTaskDefaultsReportToTaskmaster(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       &fakeTaskRunner{},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop())
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:  "task-plan",
		Locator: newAgentLocator("plan"),
		Prompt:  "Plan the goal",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.ReportTo != newAgentLocator(taskmasterAgentID) {
		t.Fatalf("out.ReportTo = %+v, want default taskmaster locator", out.ReportTo)
	}
}

func TestCoordinatorCreatesTaskmasterNotification(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{started: make(chan string, 1)}
	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: rootRunner,
		plan:       &fakeTaskRunner{result: "planned"},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-plan", newAgentLocator("plan"), newAgentLocator(taskmasterAgentID), "Plan the goal"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case prompt := <-rootRunner.started:
		if !strings.Contains(prompt, "Task task-plan completed.") ||
			!strings.Contains(prompt, "Result:") ||
			!strings.Contains(prompt, "planned") ||
			strings.Contains(prompt, "TASK ENVELOPE:") ||
			strings.Contains(prompt, `"phase"`) ||
			strings.Contains(prompt, `"source_locator"`) ||
			strings.Contains(prompt, `"report_to"`) {
			t.Fatalf("taskmaster notification = %q, want plain completion prompt", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("taskmaster did not receive notification task")
	}
}

func TestServiceFinishRemainsAvailableAfterManySchedules(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       &fakeTaskRunner{},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop())
	service.coordinator = coordinator

	for i := 0; i < 12; i++ {
		taskID := fmt.Sprintf("task-%02d", i)
		_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
			TaskID:  taskID,
			Locator: newAgentLocator("plan"),
			Prompt:  "Plan the goal",
		})
		if err != nil {
			t.Fatalf("scheduleTask(%s) error = %v", taskID, err)
		}
		if out.Status != string(taskStatusQueued) {
			t.Fatalf("scheduleTask(%s) status = %q, want queued", taskID, out.Status)
		}
	}

	_, out, err := service.finish(context.Background(), nil, finishInput{Summary: "done"})
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if out.Status != "finished" {
		t.Fatalf("finish() status = %q, want finished", out.Status)
	}
	if out.Summary != "done" {
		t.Fatalf("finish() summary = %q, want done", out.Summary)
	}
}

func TestCoordinatorSerializesPerAgent(t *testing.T) {
	t.Parallel()

	planRunner := &fakeTaskRunner{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       planRunner,
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueTask(&task{ID: "task-1", Locator: newAgentLocator("plan"), ReportTo: newAgentLocator(taskmasterAgentID), Prompt: "first"}); err != nil {
		t.Fatalf("enqueueTask(first) error = %v", err)
	}
	if err := coordinator.enqueueTask(&task{ID: "task-2", Locator: newAgentLocator("plan"), ReportTo: newAgentLocator(taskmasterAgentID), Prompt: "second"}); err != nil {
		t.Fatalf("enqueueTask(second) error = %v", err)
	}

	select {
	case got := <-planRunner.started:
		if got != "first" {
			t.Fatalf("first started prompt = %q, want first", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first plan task did not start")
	}
	select {
	case got := <-planRunner.started:
		t.Fatalf("second prompt %q started before first finished", got)
	default:
	}

	close(planRunner.release)

	select {
	case got := <-planRunner.started:
		if got != "second" {
			t.Fatalf("second started prompt = %q, want second", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second plan task did not start after first completed")
	}
}

func TestCoordinatorRunsDifferentAgentsConcurrently(t *testing.T) {
	t.Parallel()

	planRunner := &fakeTaskRunner{started: make(chan string, 1), release: make(chan struct{})}
	doRunner := &fakeTaskRunner{started: make(chan string, 1), release: make(chan struct{})}
	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       planRunner,
		do:         doRunner,
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueTask(&task{ID: "task-plan", Locator: newAgentLocator("plan"), ReportTo: newAgentLocator(taskmasterAgentID), Prompt: "plan"}); err != nil {
		t.Fatalf("enqueueTask(plan) error = %v", err)
	}
	if err := coordinator.enqueueTask(&task{ID: "task-do", Locator: newAgentLocator("do"), ReportTo: newAgentLocator(taskmasterAgentID), Prompt: "do"}); err != nil {
		t.Fatalf("enqueueTask(do) error = %v", err)
	}

	select {
	case <-planRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("plan task did not start")
	}
	select {
	case <-doRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("do task did not start")
	}
	close(planRunner.release)
	close(doRunner.release)
}

func TestCoordinatorLogsDebugLifecycle(t *testing.T) {
	t.Parallel()

	logs := &lockedStringBuffer{}
	logger := newTestLogger(logs, zerolog.DebugLevel)
	coordinator, cleanup := startTestCoordinatorWithLogger(t, logger, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       &fakeTaskRunner{result: "planned"},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-plan", newAgentLocator("plan"), newAgentLocator(taskmasterAgentID), "Plan the goal"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), `"message":"notification task created"`)
	})
	got := logs.String()
	for _, want := range []string{
		`"message":"task enqueued"`,
		`"message":"task started"`,
		`"message":"task completed"`,
		`"message":"notification task created"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs = %q, want %s", got, want)
		}
	}
	for _, want := range []string{
		`"locator":{"type":"agent","id":"plan"}`,
		`"report_to":{"type":"agent","id":"taskmaster"}`,
		`"source_locator":{"type":"agent","id":"plan"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs = %q, want %s", got, want)
		}
	}
}

func TestExecutorLogsInfoTaskLifecycle(t *testing.T) {
	t.Parallel()

	logs := &lockedStringBuffer{}
	logger := newTestLogger(logs, zerolog.InfoLevel)
	coordinator, cleanup := startTestCoordinatorWithLogger(t, logger, testRunners{
		taskmaster: &fakeTaskRunner{},
		plan:       &fakeTaskRunner{result: "planned"},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-plan", newAgentLocator("plan"), newAgentLocator(taskmasterAgentID), "Plan the goal"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), `"message":"agent finished task"`)
	})
	got := logs.String()
	for _, want := range []string{
		`"level":"info"`,
		`"agent_id":"plan"`,
		`"task_id":"task-plan"`,
		`"message":"agent received task"`,
		`"task":"Plan the goal"`,
		`"message":"agent finished task"`,
		`"result":"planned"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs = %q, want %s", got, want)
		}
	}
}

func TestCoordinatorWaitsForTerminalTaskmasterTask(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	rootRunner := &fakeTaskRunner{
		started: make(chan string, 1),
		release: release,
	}
	coordinator, cleanup := startTestCoordinator(t, testRunners{
		taskmaster: rootRunner,
		plan:       &fakeTaskRunner{},
		do:         &fakeTaskRunner{},
		check:      &fakeTaskRunner{},
		act:        &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueInitialGoal("test goal"); err != nil {
		t.Fatalf("enqueueInitialGoal() error = %v", err)
	}
	select {
	case <-rootRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("taskmaster task did not start")
	}
	if err := coordinator.finish("done"); err != nil {
		t.Fatalf("finish() error = %v", err)
	}

	waitDone := make(chan runResult, 1)
	waitErr := make(chan error, 1)
	go func() {
		result, err := coordinator.waitResult(context.Background())
		if err != nil {
			waitErr <- err
			return
		}
		waitDone <- result
	}()

	select {
	case result := <-waitDone:
		t.Fatalf("waitResult() returned early with %+v", result)
	case err := <-waitErr:
		t.Fatalf("waitResult() returned error early: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	select {
	case result := <-waitDone:
		if result.Summary != "done" {
			t.Fatalf("result.Summary = %q, want done", result.Summary)
		}
	case err := <-waitErr:
		t.Fatalf("waitResult() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("waitResult() did not return after taskmaster task completed")
	}
}

func newTestLogger(writer *lockedStringBuffer, level zerolog.Level) zerolog.Logger {
	return zerolog.New(writer).Level(level)
}

type testRunners struct {
	taskmaster *fakeTaskRunner
	plan       *fakeTaskRunner
	do         *fakeTaskRunner
	check      *fakeTaskRunner
	act        *fakeTaskRunner
}

func startTestCoordinator(t *testing.T, runners testRunners) (*coordinator, func()) {
	t.Helper()
	return startTestCoordinatorWithLogger(t, zerolog.Nop(), runners)
}

func startTestCoordinatorWithLogger(t *testing.T, logger zerolog.Logger, runners testRunners) (*coordinator, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	coordinator, err := newCoordinator(logger, map[string]taskRunner{
		taskmasterAgentID: runners.taskmaster,
		"plan":            runners.plan,
		"do":              runners.do,
		"check":           runners.check,
		"act":             runners.act,
	})
	if err != nil {
		t.Fatalf("newCoordinator() error = %v", err)
	}
	coordinator.start(ctx, map[string]taskRunner{
		taskmasterAgentID: runners.taskmaster,
		"plan":            runners.plan,
		"do":              runners.do,
		"check":           runners.check,
		"act":             runners.act,
	})
	return coordinator, func() {
		cancel()
		coordinator.wait()
	}
}

type fakeTaskRunner struct {
	mu      sync.Mutex
	result  string
	err     error
	started chan string
	release chan struct{}
}

func (r *fakeTaskRunner) RunTask(ctx context.Context, _ string, taskText string) (string, error) {
	if r.started != nil {
		r.started <- taskText
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

type lockedStringBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedStringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedStringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
