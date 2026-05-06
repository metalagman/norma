package taskmaster

import (
	"bytes"
	"context"
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

	service := newService(zerolog.Nop(), 4)
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
			!strings.Contains(prompt, `"type": "task_completion"`) ||
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
