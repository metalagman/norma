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
		"lowercase `pass` or `fail`",
		"lowercase `close` or `replan`",
		"If act returns `replan`, more planning is required before further execution.",
		"You decide the next child task yourself from the task envelopes.",
		"Do not read files, execute scripts, or perform worker work yourself.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"`continue`",
		"call taskmaster.finish with a concise replan-required summary",
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
		"lowercase `pass`",
		"lowercase `fail`",
	} {
		if !strings.Contains(checkInstruction, want) {
			t.Fatalf("check instruction = %q, want substring %q", checkInstruction, want)
		}
	}
	if strings.Contains(checkInstruction, "PASS") || strings.Contains(checkInstruction, "FAIL") {
		t.Fatalf("check instruction = %q, want lowercase literals only", checkInstruction)
	}

	actInstruction := childAgentInstructions["act"]
	for _, want := range []string{
		"advisory input for the root taskmaster agent",
		"If the verdict is `pass`, return lowercase `close`.",
		"If the verdict is `fail`, return lowercase `replan`",
		"never return `rollback`",
	} {
		if !strings.Contains(actInstruction, want) {
			t.Fatalf("act instruction = %q, want substring %q", actInstruction, want)
		}
	}
	if strings.Contains(actInstruction, "`continue`") {
		t.Fatalf("act instruction = %q, do not want continue literal", actInstruction)
	}
}

func TestFormatInitialGoalTaskInputIncludesPDCAPolicy(t *testing.T) {
	t.Parallel()

	got := formatInitialGoalTaskInput("ship it")
	for _, want := range []string{
		"GOAL TASK:",
		"ship it",
		"PDCA MODE:",
		"plan -> do -> check -> act",
		"Start with plan for iteration 1.",
		"The check phase returns lowercase `pass` or `fail`.",
		"The act phase returns lowercase `close` or `replan`.",
		"The root taskmaster agent decides the next task",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatInitialGoalTaskInput() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
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
		ID:      "task-plan",
		Locator: newAgentLocator("plan"),
		ReplyTo: newAgentLocator(taskmasterAgentID),
		Status:  taskStatusCompleted,
		Output:  "planned",
	})
	for _, want := range []string{
		"TASK ENVELOPE:",
		"This is the completion of one strict PDCA phase.",
		"Use it to decide the next child task or to finish the run.",
		`"phase": "plan"`,
		`"source_task_id": "task-plan"`,
		`"status": "completed"`,
		`"result": "planned"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatNotificationTaskInput() = %q, want substring %q", got, want)
		}
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
	done := make(chan struct{})
	go func() {
		<-planRunner.started
		close(done)
	}()

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:  "task-plan",
		Locator: newAgentLocator("plan"),
		Task:    "Plan the goal",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.Status != string(taskStatusQueued) {
		t.Fatalf("out.Status = %q, want queued", out.Status)
	}
	select {
	case <-done:
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
		if !strings.Contains(prompt, "TASK ENVELOPE:") ||
			!strings.Contains(prompt, "strict PDCA phase") ||
			!strings.Contains(prompt, `"type": "task_completion"`) ||
			!strings.Contains(prompt, `"phase": "plan"`) ||
			!strings.Contains(prompt, `"source_task_id": "task-plan"`) ||
			!strings.Contains(prompt, `"source_locator": {`) ||
			!strings.Contains(prompt, `"id": "plan"`) ||
			!strings.Contains(prompt, `"reply_to": {`) ||
			!strings.Contains(prompt, `"status": "completed"`) ||
			!strings.Contains(prompt, `"result": "planned"`) {
			t.Fatalf("taskmaster notification = %q, want completion envelope", prompt)
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
			Task:    "Plan the goal",
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

	if err := coordinator.enqueueTask(&task{ID: "task-1", Kind: taskKindAgent, Locator: newAgentLocator("plan"), ReplyTo: newAgentLocator(taskmasterAgentID), Input: "first"}); err != nil {
		t.Fatalf("enqueueTask(first) error = %v", err)
	}
	if err := coordinator.enqueueTask(&task{ID: "task-2", Kind: taskKindAgent, Locator: newAgentLocator("plan"), ReplyTo: newAgentLocator(taskmasterAgentID), Input: "second"}); err != nil {
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

	if err := coordinator.enqueueTask(&task{ID: "task-plan", Kind: taskKindAgent, Locator: newAgentLocator("plan"), ReplyTo: newAgentLocator(taskmasterAgentID), Input: "plan"}); err != nil {
		t.Fatalf("enqueueTask(plan) error = %v", err)
	}
	if err := coordinator.enqueueTask(&task{ID: "task-do", Kind: taskKindAgent, Locator: newAgentLocator("do"), ReplyTo: newAgentLocator(taskmasterAgentID), Input: "do"}); err != nil {
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
		`"reply_to":{"type":"agent","id":"taskmaster"}`,
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
