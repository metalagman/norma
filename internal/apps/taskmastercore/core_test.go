package taskmastercore

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

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
	if out.ReportTo != NewAgentLocator(rootAgentID) {
		t.Fatalf("out.ReportTo = %+v, want taskmaster root", out.ReportTo)
	}
	if out.SessionID != "session-a" {
		t.Fatalf("out.SessionID = %q, want session-a", out.SessionID)
	}
}

func TestScheduleTaskAllowsProviderReport(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		reportLocators: map[string]bool{locatorKey(NewHumanOutputLocator()): true},
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
		ReportTo:  ptrLocator(NewHumanOutputLocator()),
		Prompt:    "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
	}
	if out.ReportTo != NewHumanOutputLocator() {
		t.Fatalf("out.ReportTo = %+v, want human output locator", out.ReportTo)
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
		Source:    NewCLILocator("cli"),
	}); err != nil {
		t.Fatalf("ingest() error = %v", err)
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

	external := NewLocator("chat", "telegram", "chat-42")
	provider := &fakeProvider{
		targetLocators:  map[string]bool{locatorKey(external): true},
		dispatchStarted: make(chan DispatchRequest, 1),
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
		if req.Locator != external {
			t.Fatalf("dispatch locator = %+v, want %+v", req.Locator, external)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive external dispatch")
	}
}

func TestWaitResultReturnsLatestRootOutputOnContextDone(t *testing.T) {
	t.Parallel()

	rootRunner := &fakeTaskRunner{result: "latest root summary"}
	coordinator, cleanup := startTestCoordinator(t, zerolog.Nop(), testConfig(nil), map[string]taskRunner{
		rootAgentID: rootRunner,
		"worker":    &fakeTaskRunner{},
	})
	defer cleanup()

	if err := coordinator.ingest(IngressRequest{
		ID:        "ingress-1",
		SessionID: "session-a",
		Prompt:    "initial goal",
		Source:    NewCLILocator("cli"),
	}); err != nil {
		t.Fatalf("ingest() error = %v", err)
	}
	waitForCondition(t, func() bool {
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		return coordinator.latestRootOutput == "latest root summary"
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := coordinator.waitResult(ctx)
	if err != nil {
		t.Fatalf("waitResult() error = %v", err)
	}
	if result.Summary != "latest root summary" {
		t.Fatalf("result.Summary = %q, want latest root summary", result.Summary)
	}
}

func ptrLocator(locator Locator) *Locator {
	return &locator
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
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
	targetLocators map[string]bool
	reportLocators map[string]bool

	dispatchStarted chan DispatchRequest
	reports         chan ReportRequest
}

func (p *fakeProvider) SupportsTarget(locator Locator) bool {
	return p.targetLocators[locatorKey(locator)]
}

func (p *fakeProvider) SupportsReport(locator Locator) bool {
	return p.reportLocators[locatorKey(locator)]
}

func (p *fakeProvider) DispatchTask(_ context.Context, req DispatchRequest) error {
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
