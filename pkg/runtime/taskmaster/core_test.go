package taskmaster

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestLocatorStringUsesCanonicalFormat(t *testing.T) {
	t.Parallel()

	locator := NewTelegramHumanLocator(123456, 77)
	if got := locator.String(); got != "human:telegram:123456:77" {
		t.Fatalf("locator.String() = %q, want human:telegram:123456:77", got)
	}
	if got := locatorPtrString(&locator); got != "human:telegram:123456:77" {
		t.Fatalf("locatorPtrString() = %q, want human:telegram:123456:77", got)
	}
	if got := locatorPtrString(nil); got != "" {
		t.Fatalf("locatorPtrString(nil) = %q, want empty string", got)
	}
}

func TestRootOnlyConfigAllowed(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "ingress-1",
		SessionID: "session-a",
		Locator:   NewAgentLocator(rootAgentID),
		Content:   "hello",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}

func TestScheduleTaskDefaultsReportToRoot(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	runtime.coordinator.mu.Lock()
	defer runtime.coordinator.mu.Unlock()
	got := runtime.coordinator.tasks["task-worker"].task.ReportTo
	if got == nil || !reflect.DeepEqual(*got, NewAgentLocator(rootAgentID)) {
		t.Fatalf("report_to = %+v, want taskmaster root", got)
	}
}

func TestScheduleTaskAllowsExternalReportTarget(t *testing.T) {
	t.Parallel()

	target := &fakeTarget{supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true}}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewCLILogLocator()),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}

func TestEnqueueRejectsSourceOnlyTarget(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-source",
		SessionID: "session-a",
		Locator:   NewTimerSourceLocator(),
		Content:   "do work",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used as a target") {
		t.Fatalf("Enqueue() error = %v, want source-only target rejection", err)
	}
}

func TestEnqueueRejectsSourceOnlyReportTo(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-report",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewCLIInputLocator()),
		Content:   "do work",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used as report_to") {
		t.Fatalf("Enqueue() error = %v, want source-only report_to rejection", err)
	}
}

func TestCoordinatorCreatesRootNotificationWithSession(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	workerRunner := &fakeLocalRunner{result: "worker result"}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: rootRunner,
		"worker":    workerRunner,
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewAgentLocator(rootAgentID)),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case started := <-rootRunner.started:
		if started.SessionID != "session-a" {
			t.Fatalf("started.SessionID = %q, want session-a", started.SessionID)
		}
		if !strings.Contains(started.Content, "Session ID:\nsession-a") || !strings.Contains(started.Content, "worker result") {
			t.Fatalf("notification content = %q, want session and result", started.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive completion notification")
	}
}

func TestExternalTargetDispatchUsesTarget(t *testing.T) {
	t.Parallel()

	external := NewTelegramHumanLocator(42, 7)
	target := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(external): true},
		dispatchStarted:   make(chan Task, 1),
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-telegram",
		SessionID: "session-telegram",
		Locator:   external,
		Content:   "send outbound",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case task := <-target.dispatchStarted:
		if task.SessionID != "session-telegram" {
			t.Fatalf("dispatch session_id = %q, want session-telegram", task.SessionID)
		}
		if !reflect.DeepEqual(task.Locator, external) {
			t.Fatalf("dispatch locator = %+v, want %+v", task.Locator, external)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target did not receive external dispatch")
	}
}

func TestReportOnlyTargetDispatchFailsAndNotifiesRoot(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	target := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true},
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]LocalRunner{
		rootAgentID: rootRunner,
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	err := runtime.Enqueue(Task{
		ID:        "task-log-target",
		SessionID: "session-a",
		Locator:   NewCLILogLocator(),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case started := <-rootRunner.started:
		if !strings.Contains(started.Content, "unsupported dispatch") {
			t.Fatalf("notification content = %q, want unsupported dispatch failure", started.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive failure notification")
	}
}

func TestCLILogTargetLogsCanonicalLocatorStrings(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	target := NewCLILogTarget(logger)
	task := Task{
		ID:            "task-log",
		SessionID:     "session-a",
		Locator:       NewCLILogLocator(),
		SourceTaskID:  "task-worker",
		SourceLocator: ptrLocator(NewAgentLocator("worker")),
		Content:       "done",
	}

	if err := target.DispatchTask(context.Background(), task); err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if got := payload["source_locator"]; got != "agent:local:worker" {
		t.Fatalf("source_locator = %#v, want agent:local:worker", got)
	}
	if got := payload["locator"]; got != "integration:cli:log" {
		t.Fatalf("locator = %#v, want integration:cli:log", got)
	}
}

func TestShutdownResultReturnsStoppedSummary(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	runtime.coordinator.mu.Lock()
	runtime.coordinator.latestRootOutput = "latest root summary"
	runtime.coordinator.mu.Unlock()

	result, err := runtime.Shutdown(context.Background(), ShutdownInput{Cause: context.Canceled})
	if err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !result.Stopped {
		t.Fatal("result.Stopped = false, want true")
	}
	if strings.Contains(result.Summary, "latest root summary") && result.Summary == "latest root summary" {
		t.Fatalf("result.Summary = %q, want explicit stop summary instead of stale root output", result.Summary)
	}
	if !strings.Contains(result.Summary, "Run stopped") {
		t.Fatalf("result.Summary = %q, want stop summary", result.Summary)
	}
	if !strings.Contains(result.Summary, "latest root summary") {
		t.Fatalf("result.Summary = %q, want last root output as context", result.Summary)
	}
}

func TestEnqueueRejectedDuringShutdown(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	_, err := runtime.Shutdown(context.Background(), ShutdownInput{Summary: "stopped"})
	if err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	err = runtime.Enqueue(Task{
		ID:        "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Content:   "do work",
	})
	if err == nil || !strings.Contains(err.Error(), "run already finished") {
		t.Fatalf("Enqueue() error = %v, want shutdown rejection", err)
	}
}

func TestQueuedTaskDoesNotStartAfterShutdownBegins(t *testing.T) {
	t.Parallel()

	workerRunner := &fakeLocalRunner{
		started: make(chan startedTask, 2),
		release: make(chan struct{}),
	}
	runtime, err := Start(context.Background(), Config{
		RootAgentID:     rootAgentID,
		LocalRunners:    map[string]LocalRunner{rootAgentID: &fakeLocalRunner{}, "worker": workerRunner},
		DefaultReportTo: NewAgentLocator(rootAgentID),
		ReportTaskContentFormatter: func(source Task, output string, err error) string {
			if err != nil {
				return err.Error()
			}
			return output
		},
		ShutdownSummaryFormatter: func(input ShutdownSummaryInput) string { return "stopped" },
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()

	err = runtime.Enqueue(Task{ID: "task-1", SessionID: "session-a", Locator: NewAgentLocator("worker"), Content: "first"})
	if err != nil {
		t.Fatalf("Enqueue(task-1) error = %v", err)
	}
	err = runtime.Enqueue(Task{ID: "task-2", SessionID: "session-a", Locator: NewAgentLocator("worker"), Content: "second"})
	if err != nil {
		t.Fatalf("Enqueue(task-2) error = %v", err)
	}

	select {
	case started := <-workerRunner.started:
		if started.Content != "first" {
			t.Fatalf("first started content = %q, want first", started.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}

	runtime.coordinator.beginShutdown()
	runtime.cancel()
	close(workerRunner.release)
	runtime.coordinator.wait()

	select {
	case started := <-workerRunner.started:
		t.Fatalf("unexpected task started after shutdown: %+v", started)
	case <-time.After(200 * time.Millisecond):
	}
}

func ptrLocator(locator Locator) *Locator {
	return &locator
}

func startTestRuntime(t *testing.T, logger zerolog.Logger, cfg Config, runners map[string]LocalRunner) (*Runtime, func()) {
	t.Helper()

	cfg.RootAgentID = rootAgentID
	cfg.LocalRunners = runners
	cfg.DefaultReportTo = NewAgentLocator(rootAgentID)
	runtime, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return runtime, func() {
		_ = runtime.Close()
	}
}

func testConfig(targets []Target) Config {
	return Config{
		Targets: targets,
		ReportTaskContentFormatter: func(source Task, output string, err error) string {
			lines := []string{"Session ID:", source.SessionID, ""}
			if err != nil {
				lines = append(lines, "Error:", err.Error())
				return strings.Join(lines, "\n")
			}
			lines = append(lines, "Result:", output)
			return strings.Join(lines, "\n")
		},
		ShutdownSummaryFormatter: func(input ShutdownSummaryInput) string {
			lines := []string{"Run stopped."}
			if input.LastRootOutput != "" {
				lines = append(lines, "", input.LastRootOutput)
			}
			return strings.Join(lines, "\n")
		},
	}
}

const rootAgentID = "taskmaster"

type startedTask struct {
	SessionID string
	Content   string
}

type fakeLocalRunner struct {
	mu      sync.Mutex
	result  string
	err     error
	started chan startedTask
	release chan struct{}
}

func (r *fakeLocalRunner) RunTask(ctx context.Context, task Task) (string, error) {
	if r.started != nil {
		r.started <- startedTask{SessionID: task.SessionID, Content: task.Content}
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

func (r *fakeLocalRunner) Close() error {
	return nil
}

type fakeTarget struct {
	supportedLocators map[string]bool
	dispatchStarted   chan Task
}

func (t *fakeTarget) Supports(locator Locator) bool {
	return t.supportedLocators[locatorKey(locator)]
}

func (t *fakeTarget) DispatchTask(_ context.Context, task Task) error {
	if reflect.DeepEqual(task.Locator, NewCLILogLocator()) {
		return ErrUnsupported
	}
	if t.dispatchStarted != nil {
		t.dispatchStarted <- task
	}
	return nil
}
