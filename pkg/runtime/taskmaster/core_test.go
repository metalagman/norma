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

	fakeChat := NewFakeChatHumanLocator("local")
	if got := fakeChat.String(); got != "human:fakechat:local" {
		t.Fatalf("locator.String() = %q, want human:fakechat:local", got)
	}
}

func TestRootOnlyConfigAllowed(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
	})
	defer cleanup()

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator(rootAgentID),
		Content:   "hello",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}

func TestScheduleTaskDoesNotDefaultReportTo(t *testing.T) {
	t.Parallel()

	workerRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    workerRunner,
	})
	defer cleanup()

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Content:   "do work",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case started := <-workerRunner.started:
		if started.ReportTo != nil {
			t.Fatalf("report_to = %+v, want nil", started.ReportTo)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive task")
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

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewCLILogLocator()),
		Content:   "do work",
	}); err != nil {
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

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewAgentLocator(rootAgentID)),
		Content:   "do work",
	}); err != nil {
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
		if strings.Contains(started.Content, "task-worker") {
			t.Fatalf("notification content = %q, do not want public task id", started.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive completion notification")
	}
}

func TestRootTaskHonorsExplicitReportTo(t *testing.T) {
	t.Parallel()

	reportTo := NewTelegramHumanLocator(42, 9)
	logTarget := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(reportTo): true},
		dispatchStarted:   make(chan Task, 1),
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{logTarget}), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{result: "root result"},
	})
	defer cleanup()

	if err := runtime.Enqueue(Task{
		SessionID: "session-root",
		Locator:   NewAgentLocator(rootAgentID),
		ReportTo:  &reportTo,
		Content:   "root work",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case task := <-logTarget.dispatchStarted:
		if task.SessionID != "session-root" {
			t.Fatalf("dispatch session_id = %q, want session-root", task.SessionID)
		}
		if task.Content == "" || !strings.Contains(task.Content, "root result") {
			t.Fatalf("dispatch content = %q, want root result notification", task.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("report_to target did not receive root notification")
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

	if err := runtime.Enqueue(Task{
		SessionID: "session-telegram",
		Locator:   external,
		Content:   "send outbound",
	}); err != nil {
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

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewCLILogLocator(),
		ReportTo:  ptrLocator(NewAgentLocator(rootAgentID)),
		Content:   "do work",
	}); err != nil {
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
		SessionID: "session-a",
		Locator:   NewCLILogLocator(),
		Content:   "done",
	}

	if err := target.DispatchTask(context.Background(), task); err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if got := payload["locator"]; got != "integration:cli:log" {
		t.Fatalf("locator = %#v, want integration:cli:log", got)
	}
	if _, ok := payload["source_locator"]; ok {
		t.Fatalf("unexpected source_locator in log payload: %#v", payload["source_locator"])
	}
}

func TestRuntimeDoneClosesOnStop(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-runtime.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime done did not close")
	}
}

func TestEnqueueRejectedDuringShutdown(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    &fakeLocalRunner{},
	})
	defer cleanup()

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Content:   "do work",
	})
	if err == nil || !strings.Contains(err.Error(), "runtime is stopping") {
		t.Fatalf("Enqueue() error = %v, want shutdown rejection", err)
	}
}

func TestEnqueuePreservesContentWhitespace(t *testing.T) {
	t.Parallel()

	workerRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    workerRunner,
	})
	defer cleanup()

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Content:   "  do work  ",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case started := <-workerRunner.started:
		if started.Content != "  do work  " {
			t.Fatalf("started.Content = %q, want raw content", started.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive task")
	}
}

func TestQueuedTaskDoesNotStartAfterShutdownBegins(t *testing.T) {
	t.Parallel()

	workerRunner := &fakeLocalRunner{
		started: make(chan startedTask, 2),
		release: make(chan struct{}),
	}
	runtime, err := New(Config{
		RootAgentID:  rootAgentID,
		LocalRunners: map[string]LocalRunner{rootAgentID: &fakeLocalRunner{}, "worker": workerRunner},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = runtime.Stop(context.Background()) }()

	if err := runtime.Enqueue(Task{SessionID: "session-a", Locator: NewAgentLocator("worker"), Content: "first"}); err != nil {
		t.Fatalf("Enqueue(task-1) error = %v", err)
	}
	if err := runtime.Enqueue(Task{SessionID: "session-a", Locator: NewAgentLocator("worker"), Content: "second"}); err != nil {
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

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(workerRunner.release)
	}()
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case started := <-workerRunner.started:
		t.Fatalf("unexpected task started after shutdown: %+v", started)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDuplicateNormalizedLocalRunnerIDsRejected(t *testing.T) {
	t.Parallel()

	_, err := New(Config{
		RootAgentID: rootAgentID,
		LocalRunners: map[string]LocalRunner{
			rootAgentID: &fakeLocalRunner{},
			"Worker":    &fakeLocalRunner{},
			"worker":    &fakeLocalRunner{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate local runner ids") {
		t.Fatalf("New() error = %v, want duplicate normalized runner rejection", err)
	}
}

func TestMixedCaseLocalAgentLocatorRoutesToNormalizedRunner(t *testing.T) {
	t.Parallel()

	workerRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]LocalRunner{
		rootAgentID: &fakeLocalRunner{},
		"worker":    workerRunner,
	})
	defer cleanup()

	if err := runtime.Enqueue(Task{
		SessionID: "session-a",
		Locator:   NewLocator(LocatorClassAgent, LocatorTransportLocal, "Worker"),
		Content:   "do work",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case <-workerRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive task")
	}
}

func ptrLocator(locator Locator) *Locator {
	return &locator
}

func startTestRuntime(t *testing.T, logger zerolog.Logger, cfg Config, runners map[string]LocalRunner) (*Runtime, func()) {
	t.Helper()

	cfg.RootAgentID = rootAgentID
	cfg.LocalRunners = runners
	runtime, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return runtime, func() {
		_ = runtime.Stop(context.Background())
	}
}

func testConfig(targets []Target) Config {
	return Config{
		Targets: targets,
	}
}

const rootAgentID = "taskmaster"

type startedTask struct {
	SessionID string
	Locator   Locator
	ReportTo  *Locator
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
		r.started <- startedTask(task)
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
