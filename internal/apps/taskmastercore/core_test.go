package taskmastercore

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
	if got := locatorString(locator); got != "human:telegram:123456:77" {
		t.Fatalf("locatorString() = %q, want human:telegram:123456:77", got)
	}
	if got := locatorPtrString(&locator); got != "human:telegram:123456:77" {
		t.Fatalf("locatorPtrString() = %q, want human:telegram:123456:77", got)
	}
	if got := locatorPtrString(nil); got != "" {
		t.Fatalf("locatorPtrString(nil) = %q, want empty string", got)
	}
}

func TestScheduleTaskDefaultsReportToRoot(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator(rootAgentID), true)
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:    "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if !reflect.DeepEqual(out.ReportTo, NewAgentLocator(rootAgentID)) {
		t.Fatalf("out.ReportTo = %+v, want taskmaster root", out.ReportTo)
	}
	if out.SessionID != "session-a" {
		t.Fatalf("out.SessionID = %q, want session-a", out.SessionID)
	}
}

func TestScheduleTaskAllowsProviderReport(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true},
	}
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig([]Provider{provider}), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator(rootAgentID), true)
	service.coordinator = coordinator

	_, out, err := service.scheduleTask(context.Background(), nil, scheduleTaskInput{
		TaskID:    "task-worker",
		SessionID: "session-a",
		Locator:   NewAgentLocator("worker"),
		ReportTo:  ptrLocator(NewCLILogLocator()),
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if !reflect.DeepEqual(out.ReportTo, NewCLILogLocator()) {
		t.Fatalf("out.ReportTo = %+v, want cli log locator", out.ReportTo)
	}
}

func TestRootOnlyConfigAllowed(t *testing.T) {
	t.Parallel()

	cfg := testConfig(nil)
	cfg.ChildAgents = nil
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), cfg, map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.ingest(IngressRequest{
		ID:        "ingress-1",
		SessionID: "session-a",
		Prompt:    "hello",
		Source:    NewCLIInputLocator(),
	}); err != nil {
		t.Fatalf("ingest() error = %v", err)
	}
}

func TestScheduleTaskRejectsSourceOnlyTarget(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	err := coordinator.scheduleTask("task-source", "session-a", NewTimerSourceLocator(), NewAgentLocator(rootAgentID), "do work")
	if err == nil || !strings.Contains(err.Error(), "cannot be used as a target") {
		t.Fatalf("scheduleTask() error = %v, want source-only target rejection", err)
	}
}

func TestScheduleTaskRejectsSourceOnlyReportTo(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	err := coordinator.scheduleTask("task-report", "session-a", NewAgentLocator("worker"), NewCLIInputLocator(), "do work")
	if err == nil || !strings.Contains(err.Error(), "cannot be used as report_to") {
		t.Fatalf("scheduleTask() error = %v, want source-only report_to rejection", err)
	}
}

func TestFinishToolCanBeDisabled(t *testing.T) {
	t.Parallel()

	cfg := testConfig(nil)
	cfg.AllowFinishTool = false
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), cfg, map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	service := newService(zerolog.Nop(), NewAgentLocator(rootAgentID), false)
	service.coordinator = coordinator

	_, out, err := service.finish(context.Background(), nil, finishInput{Summary: "done"})
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if out.Status != "error" || !strings.Contains(out.Summary, "finish tool is disabled") {
		t.Fatalf("finish() output = %+v", out)
	}
}

func TestCoordinatorCreatesRootNotificationWithSession(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{started: make(chan startedTask, 1)}
	workerRunner := &fakeTaskRunner{result: "worker result"}
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: rootRunner,
		"worker":    workerRunner,
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-worker", "session-a", NewAgentLocator("worker"), NewAgentLocator(rootAgentID), "do work"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case started := <-rootRunner.started:
		if started.SessionID != "session-a" {
			t.Fatalf("started.SessionID = %q, want session-a", started.SessionID)
		}
		if !strings.Contains(started.Prompt, "Session ID:\nsession-a") || !strings.Contains(started.Prompt, "worker result") {
			t.Fatalf("notification prompt = %q, want session and result", started.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive completion notification")
	}
}

func TestExternalTargetDispatchUsesProvider(t *testing.T) {
	t.Parallel()

	external := NewTelegramHumanLocator(42, 7)
	provider := &fakeProvider{
		supportedLocators: map[string]bool{locatorKey(external): true},
		dispatchStarted:   make(chan DispatchRequest, 1),
	}
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig([]Provider{provider}), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-telegram", "session-telegram", external, NewAgentLocator(rootAgentID), "send outbound"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case req := <-provider.dispatchStarted:
		if req.SessionID != "session-telegram" {
			t.Fatalf("dispatch session_id = %q, want session-telegram", req.SessionID)
		}
		if !reflect.DeepEqual(req.Locator, external) {
			t.Fatalf("dispatch locator = %+v, want %+v", req.Locator, external)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive external dispatch")
	}
}

func TestReportOnlyTargetDispatchFailsAndNotifiesRoot(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{started: make(chan startedTask, 1)}
	provider := &fakeProvider{
		supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true},
	}
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig([]Provider{provider}), map[string]taskRunner{
		rootAgentID: rootRunner,
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleTask("task-log-target", "session-a", NewCLILogLocator(), NewAgentLocator(rootAgentID), "do work"); err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}

	select {
	case started := <-rootRunner.started:
		if !strings.Contains(started.Prompt, "unsupported dispatch") {
			t.Fatalf("notification prompt = %q, want unsupported dispatch failure", started.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root did not receive failure notification")
	}
}

func TestCLILogProviderLogsCanonicalLocatorStrings(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	provider := NewCLILogProvider(logger)
	req := ReportRequest{
		TaskID:        "task-log",
		SessionID:     "session-a",
		SourceLocator: NewAgentLocator("worker"),
		ReportTo:      NewCLILogLocator(),
		Prompt:        "done",
	}

	if err := provider.DeliverReport(context.Background(), req); err != nil {
		t.Fatalf("DeliverReport() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if got := payload["source_locator"]; got != "agent:local:worker" {
		t.Fatalf("source_locator = %#v, want agent:local:worker", got)
	}
	if got := payload["report_to"]; got != "integration:cli:log" {
		t.Fatalf("report_to = %#v, want integration:cli:log", got)
	}
}

func TestContextDoneResultReturnsStoppedSummary(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	coordinator.mu.Lock()
	coordinator.latestRootOutput = "latest root summary"
	coordinator.mu.Unlock()

	result, err := coordinator.contextDoneResult(context.Canceled)
	if err != nil {
		t.Fatalf("contextDoneResult() error = %v", err)
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

func TestScheduleAndIngressRejectedDuringShutdown(t *testing.T) {
	t.Parallel()

	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	coordinator.beginShutdown()

	if err := coordinator.scheduleTask("task-worker", "session-a", NewAgentLocator("worker"), NewAgentLocator(rootAgentID), "do work"); err == nil || !strings.Contains(err.Error(), "run already finished") {
		t.Fatalf("scheduleTask() error = %v, want shutdown rejection", err)
	}
	if err := coordinator.ingest(IngressRequest{
		ID:        "ingress-1",
		SessionID: "session-a",
		Prompt:    "hello",
		Source:    NewCLIInputLocator(),
	}); err == nil || !strings.Contains(err.Error(), "run already finished") {
		t.Fatalf("ingest() error = %v, want shutdown rejection", err)
	}
}

func TestQueuedTaskDoesNotStartAfterShutdownBegins(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerRunner := &fakeTaskRunner{
		started: make(chan startedTask, 2),
		release: make(chan struct{}),
	}
	coordinator, err := newCoordinator(zerolog.Nop(), testConfig(nil))
	if err != nil {
		t.Fatalf("newCoordinator() error = %v", err)
	}
	coordinator.start(ctx, map[string]taskRunner{
		rootAgentID: &fakeTaskRunner{},
		"worker":    workerRunner,
	})
	defer coordinator.wait()

	if err := coordinator.scheduleTask("task-1", "session-a", NewAgentLocator("worker"), NewAgentLocator(rootAgentID), "first"); err != nil {
		t.Fatalf("scheduleTask(task-1) error = %v", err)
	}
	if err := coordinator.scheduleTask("task-2", "session-a", NewAgentLocator("worker"), NewAgentLocator(rootAgentID), "second"); err != nil {
		t.Fatalf("scheduleTask(task-2) error = %v", err)
	}

	select {
	case started := <-workerRunner.started:
		if started.Prompt != "first" {
			t.Fatalf("first started prompt = %q, want first", started.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}

	coordinator.beginShutdown()
	cancel()
	close(workerRunner.release)

	select {
	case started := <-workerRunner.started:
		t.Fatalf("unexpected task started after shutdown: %+v", started)
	case <-time.After(200 * time.Millisecond):
	}
}

func ptrLocator(locator Locator) *Locator {
	return &locator
}

func startTestCoordinator(t *testing.T, logger zerolog.Logger, cfg Config, runners map[string]taskRunner) (*coordinator, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		t.Fatalf("newCoordinator() error = %v", err)
	}
	coordinator.start(ctx, runners)
	return coordinator, func() {
		cancel()
		coordinator.wait()
	}
}

func testConfig(providers []Provider) Config {
	return Config{
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
		DefaultReportTo: NewAgentLocator(rootAgentID),
		AllowFinishTool: true,
		Providers:       providers,
		IngressPromptFormatter: func(req IngressRequest) string {
			return strings.Join([]string{"Session ID:", req.SessionID, "", "Prompt:", req.Prompt}, "\n")
		},
		CompletionPromptFormatter: func(input CompletionPromptInput) string {
			if input.Error != "" {
				return strings.Join([]string{"Session ID:", input.SessionID, "", "Error:", input.Error}, "\n")
			}
			return strings.Join([]string{"Session ID:", input.SessionID, "", "Result:", input.Output}, "\n")
		},
		ContextDoneSummaryFormatter: func(input ContextDoneSummaryInput) string {
			lines := []string{"Run stopped."}
			if input.LastRootOutput != "" {
				lines = append(lines, "", input.LastRootOutput)
			}
			return strings.Join(lines, "\n")
		},
		FinishOnContextDone: true,
	}
}

const rootAgentID = "taskmaster"

type startedTask struct {
	SessionID string
	Prompt    string
}

type fakeTaskRunner struct {
	mu      sync.Mutex
	result  string
	err     error
	started chan startedTask
	release chan struct{}
}

func (r *fakeTaskRunner) RunTask(ctx context.Context, sessionID string, _ string, taskText string) (string, error) {
	if r.started != nil {
		r.started <- startedTask{SessionID: sessionID, Prompt: taskText}
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

type fakeProvider struct {
	supportedLocators map[string]bool

	dispatchStarted chan DispatchRequest
	reports         chan ReportRequest
}

func (p *fakeProvider) Supports(locator Locator) bool {
	return p.supportedLocators[locatorKey(locator)]
}

func (p *fakeProvider) DispatchTask(_ context.Context, req DispatchRequest) error {
	if reflect.DeepEqual(req.Locator, NewCLILogLocator()) {
		return ErrUnsupported
	}
	if p.dispatchStarted != nil {
		p.dispatchStarted <- req
	}
	return nil
}

func (p *fakeProvider) DeliverReport(_ context.Context, req ReportRequest) error {
	if p.reports != nil {
		p.reports <- req
	}
	return nil
}
