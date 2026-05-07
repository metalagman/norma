package taskmastercore

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestScheduleTaskDefaultsReportToRoot(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), false)
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator("taskmaster"))
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:  "task-worker",
		Locator: NewAgentLocator("worker"),
		Prompt:  "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.ReportTo != NewAgentLocator("taskmaster") {
		t.Fatalf("out.ReportTo = %+v, want taskmaster", out.ReportTo)
	}
}

func TestScheduleTaskAllowsHumanOutputWhenEnabled(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), true)
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator("taskmaster"))
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:   "task-worker",
		Locator:  NewAgentLocator("worker"),
		ReportTo: ptrLocator(NewHumanOutputLocator()),
		Prompt:   "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.ReportTo != NewHumanOutputLocator() {
		t.Fatalf("out.ReportTo = %+v, want human_output current_log", out.ReportTo)
	}
}

func TestScheduleTaskRejectsHumanOutputWhenDisabled(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), false)
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator("taskmaster"))
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:   "task-worker",
		Locator:  NewAgentLocator("worker"),
		ReportTo: ptrLocator(NewHumanOutputLocator()),
		Prompt:   "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.Status != "error" || !strings.Contains(out.Message, `unsupported report_to.type "human_output"`) {
		t.Fatalf("scheduleTask() output = %+v", out)
	}
}

func TestCoordinatorCreatesRootNotification(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{started: make(chan string, 1)}
	workerRunner := &fakeTaskRunner{result: "worker result"}
	coordinator, cleanup := startTestCoordinatorWithRunners(t, zerolog.Nop(), false, rootRunner, workerRunner)
	defer cleanup()

	if err := coordinator.scheduleTask("task-worker", NewAgentLocator("worker"), NewAgentLocator("taskmaster"), "do work"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case prompt := <-rootRunner.started:
		if !strings.Contains(prompt, "Task task-worker completed.") || !strings.Contains(prompt, "worker result") {
			t.Fatalf("notification prompt = %q, want completion prompt", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive completion notification")
	}
}

func TestCoordinatorHumanOutputSkipsRootNotification(t *testing.T) {
	t.Parallel()

	logs := &lockedStringBuffer{}
	logger := zerolog.New(logs).Level(zerolog.InfoLevel)
	rootRunner := &fakeTaskRunner{started: make(chan string, 1)}
	workerRunner := &fakeTaskRunner{result: "worker result"}
	coordinator, cleanup := startTestCoordinatorWithRunners(t, logger, true, rootRunner, workerRunner)
	defer cleanup()

	if err := coordinator.scheduleTask("task-worker", NewAgentLocator("worker"), NewHumanOutputLocator(), "do work"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case prompt := <-rootRunner.started:
		t.Fatalf("root unexpectedly got prompt %q from human_output report", prompt)
	case <-time.After(150 * time.Millisecond):
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "human output delivered") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("logs = %q, want human output delivered entry", logs.String())
}

func TestQueuedRootTaskSkippedAfterFinish(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	coordinator, cleanup := startTestCoordinatorWithRunners(t, zerolog.Nop(), false, rootRunner, &fakeTaskRunner{})
	defer cleanup()

	if err := coordinator.enqueueInitialGoal("initial goal"); err != nil {
		t.Fatalf("enqueueInitialGoal() error = %v", err)
	}
	select {
	case <-rootRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("initial root task did not start")
	}

	if err := coordinator.enqueueBackgroundGoal("hello world"); err != nil {
		t.Fatalf("enqueueBackgroundGoal() error = %v", err)
	}
	if err := coordinator.finish("done"); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	close(rootRunner.release)

	result, err := coordinator.waitResult(context.Background())
	if err != nil {
		t.Fatalf("waitResult() error = %v", err)
	}
	if result.Summary != "done" {
		t.Fatalf("result.Summary = %q, want done", result.Summary)
	}

	select {
	case prompt := <-rootRunner.started:
		t.Fatalf("unexpected second root task started with prompt %q after finish", prompt)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestCanceledTaskDuringShutdownIsNotTreatedAsFailure(t *testing.T) {
	t.Parallel()

	logs := &lockedStringBuffer{}
	logger := zerolog.New(logs).Level(zerolog.DebugLevel)
	rootRunner := &fakeTaskRunner{}
	workerRunner := &fakeTaskRunner{
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{
		RootAgentID: rootAgentID,
		RootAgent: AgentConfig{
			Name:        "root",
			Instruction: "root",
		},
		ChildAgents: map[string]AgentConfig{
			"worker": {
				Name:        "worker",
				Instruction: "worker",
			},
		},
		DefaultReportTo:      NewAgentLocator(rootAgentID),
		AllowHumanOutputSink: false,
		GoalPromptFormatter:  func(goal string) string { return goal },
	}
	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		t.Fatalf("newCoordinator() error = %v", err)
	}
	coordinator.start(ctx, map[string]taskRunner{
		rootAgentID: rootRunner,
		"worker":    workerRunner,
	})
	defer coordinator.wait()

	if err := coordinator.scheduleTask("task-worker", NewAgentLocator("worker"), NewAgentLocator("taskmaster"), "do work"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	waitForString(t, logs, `"message":"task started"`)
	coordinator.beginShutdown()
	cancel()

	waitForString(t, logs, `"message":"task canceled during shutdown"`)
	if strings.Contains(logs.String(), `"message":"task failed"`) {
		t.Fatalf("logs = %q, do not want task failed log for expected shutdown cancel", logs.String())
	}
}

func ptrLocator(locator Locator) *Locator {
	return &locator
}

func waitForString(t *testing.T, logs *lockedStringBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("logs = %q, want substring %q", logs.String(), needle)
}

func startTestCoordinator(t *testing.T, logger zerolog.Logger, allowHumanOutput bool) (*coordinator, func()) {
	t.Helper()
	return startTestCoordinatorWithRunners(t, logger, allowHumanOutput, &fakeTaskRunner{}, &fakeTaskRunner{})
}

func startTestCoordinatorWithRunners(t *testing.T, logger zerolog.Logger, allowHumanOutput bool, rootRunner, workerRunner *fakeTaskRunner) (*coordinator, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{
		RootAgentID: rootAgentID,
		RootAgent: AgentConfig{
			Name:        "root",
			Instruction: "root",
		},
		ChildAgents: map[string]AgentConfig{
			"worker": {
				Name:        "worker",
				Instruction: "worker",
			},
		},
		DefaultReportTo:      NewAgentLocator(rootAgentID),
		AllowHumanOutputSink: allowHumanOutput,
		GoalPromptFormatter:  func(goal string) string { return goal },
	}
	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		t.Fatalf("newCoordinator() error = %v", err)
	}
	runners := map[string]taskRunner{
		rootAgentID: rootRunner,
		"worker":    workerRunner,
	}
	coordinator.start(ctx, runners)
	return coordinator, func() {
		cancel()
		coordinator.wait()
	}
}

const rootAgentID = "taskmaster"

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
