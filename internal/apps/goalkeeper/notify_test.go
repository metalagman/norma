package goalkeeper

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNotifyServiceScheduleJobQueuesImmediately(t *testing.T) {
	t.Parallel()

	planRunner := &fakeNotifyRunner{
		result:  "planned",
		started: make(chan string, 1),
		release: make(chan struct{}),
	}
	schedulerRunner := &fakeNotifyRunner{started: make(chan string, 1)}
	coordinator, cleanup := startNotifyTestCoordinator(t, notifyTestRunners{
		scheduler: schedulerRunner,
		plan:      planRunner,
		do:        &fakeNotifyRunner{},
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	service := newNotifyService(zerolog.Nop(), 4)
	service.coordinator = coordinator
	done := make(chan struct{})
	go func() {
		<-planRunner.started
		close(done)
	}()

	_, out, err := service.scheduleJob(context.Background(), nil, scheduleJobInput{
		JobID:         "job-plan",
		TargetAgentID: "plan",
		Task:          "Plan the goal",
	})
	if err != nil {
		t.Fatalf("scheduleJob() error = %v", err)
	}
	if out.Status != string(notifyJobStatusQueued) {
		t.Fatalf("out.Status = %q, want queued", out.Status)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker job was not started")
	}
	select {
	case prompt := <-schedulerRunner.started:
		t.Fatalf("scheduler got prompt %q before worker finished, want async scheduling", prompt)
	default:
	}
	close(planRunner.release)
}

func TestNotifyCoordinatorCreatesSchedulerNotification(t *testing.T) {
	t.Parallel()

	schedulerRunner := &fakeNotifyRunner{started: make(chan string, 1)}
	coordinator, cleanup := startNotifyTestCoordinator(t, notifyTestRunners{
		scheduler: schedulerRunner,
		plan:      &fakeNotifyRunner{result: "planned"},
		do:        &fakeNotifyRunner{},
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleWorkerJob("job-plan", "plan", "Plan the goal"); err != nil {
		t.Fatalf("scheduleWorkerJob() error = %v", err)
	}

	select {
	case prompt := <-schedulerRunner.started:
		if !strings.Contains(prompt, "JOB NOTIFICATION:") ||
			!strings.Contains(prompt, "source_job_id: job-plan") ||
			!strings.Contains(prompt, "source_agent_id: plan") ||
			!strings.Contains(prompt, "status: completed") ||
			!strings.Contains(prompt, "planned") {
			t.Fatalf("scheduler notification = %q, want completion payload", prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not receive notification job")
	}
}

func TestNotifyCoordinatorSerializesPerAgent(t *testing.T) {
	t.Parallel()

	planRunner := &fakeNotifyRunner{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	coordinator, cleanup := startNotifyTestCoordinator(t, notifyTestRunners{
		scheduler: &fakeNotifyRunner{},
		plan:      planRunner,
		do:        &fakeNotifyRunner{},
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueJob(&notifyJob{ID: "job-1", Kind: notifyJobKindAgent, TargetAgentID: "plan", Input: "first"}); err != nil {
		t.Fatalf("enqueueJob(first) error = %v", err)
	}
	if err := coordinator.enqueueJob(&notifyJob{ID: "job-2", Kind: notifyJobKindAgent, TargetAgentID: "plan", Input: "second"}); err != nil {
		t.Fatalf("enqueueJob(second) error = %v", err)
	}

	select {
	case got := <-planRunner.started:
		if got != "first" {
			t.Fatalf("first started prompt = %q, want first", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first plan job did not start")
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
		t.Fatal("second plan job did not start after first completed")
	}
}

func TestNotifyCoordinatorRunsDifferentAgentsConcurrently(t *testing.T) {
	t.Parallel()

	planRunner := &fakeNotifyRunner{started: make(chan string, 1), release: make(chan struct{})}
	doRunner := &fakeNotifyRunner{started: make(chan string, 1), release: make(chan struct{})}
	coordinator, cleanup := startNotifyTestCoordinator(t, notifyTestRunners{
		scheduler: &fakeNotifyRunner{},
		plan:      planRunner,
		do:        doRunner,
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueJob(&notifyJob{ID: "job-plan", Kind: notifyJobKindAgent, TargetAgentID: "plan", Input: "plan"}); err != nil {
		t.Fatalf("enqueueJob(plan) error = %v", err)
	}
	if err := coordinator.enqueueJob(&notifyJob{ID: "job-do", Kind: notifyJobKindAgent, TargetAgentID: "do", Input: "do"}); err != nil {
		t.Fatalf("enqueueJob(do) error = %v", err)
	}

	select {
	case <-planRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("plan job did not start")
	}
	select {
	case <-doRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("do job did not start")
	}
	close(planRunner.release)
	close(doRunner.release)
}

func TestNotifyCoordinatorLogsDebugLifecycle(t *testing.T) {
	t.Parallel()

	logs := &lockedStringBuffer{}
	logger := newTestLogger(logs, zerolog.DebugLevel)
	coordinator, cleanup := startNotifyTestCoordinatorWithLogger(t, logger, notifyTestRunners{
		scheduler: &fakeNotifyRunner{},
		plan:      &fakeNotifyRunner{result: "planned"},
		do:        &fakeNotifyRunner{},
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	if err := coordinator.scheduleWorkerJob("job-plan", "plan", "Plan the goal"); err != nil {
		t.Fatalf("scheduleWorkerJob() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), `"message":"notification job created"`)
	})
	got := logs.String()
	for _, want := range []string{
		`"message":"job enqueued"`,
		`"message":"job started"`,
		`"message":"job completed"`,
		`"message":"notification job created"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs = %q, want %s", got, want)
		}
	}
}

func TestNotifyCoordinatorWaitsForTerminalSchedulerJob(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	schedulerRunner := &fakeNotifyRunner{
		started: make(chan string, 1),
		release: release,
		onRun: func() {
			// Mimic the scheduler calling goalkeeper.finish from inside the prompt turn.
		},
	}
	coordinator, cleanup := startNotifyTestCoordinator(t, notifyTestRunners{
		scheduler: schedulerRunner,
		plan:      &fakeNotifyRunner{},
		do:        &fakeNotifyRunner{},
		check:     &fakeNotifyRunner{},
		act:       &fakeNotifyRunner{},
	})
	defer cleanup()

	if err := coordinator.enqueueInitialGoal("test goal"); err != nil {
		t.Fatalf("enqueueInitialGoal() error = %v", err)
	}
	select {
	case <-schedulerRunner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler job did not start")
	}
	if err := coordinator.finish("done"); err != nil {
		t.Fatalf("finish() error = %v", err)
	}

	waitDone := make(chan notifyRunResult, 1)
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
		t.Fatal("waitResult() did not return after scheduler job completed")
	}
}

type notifyTestRunners struct {
	scheduler *fakeNotifyRunner
	plan      *fakeNotifyRunner
	do        *fakeNotifyRunner
	check     *fakeNotifyRunner
	act       *fakeNotifyRunner
}

func startNotifyTestCoordinator(t *testing.T, runners notifyTestRunners) (*notifyCoordinator, func()) {
	t.Helper()
	return startNotifyTestCoordinatorWithLogger(t, zerolog.Nop(), runners)
}

func startNotifyTestCoordinatorWithLogger(t *testing.T, logger zerolog.Logger, runners notifyTestRunners) (*notifyCoordinator, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	coordinator, err := newNotifyCoordinator(logger, map[string]notifyJobRunner{
		schedulerAgentID: runners.scheduler,
		"plan":           runners.plan,
		"do":             runners.do,
		"check":          runners.check,
		"act":            runners.act,
	})
	if err != nil {
		t.Fatalf("newNotifyCoordinator() error = %v", err)
	}
	coordinator.start(ctx, map[string]notifyJobRunner{
		schedulerAgentID: runners.scheduler,
		"plan":           runners.plan,
		"do":             runners.do,
		"check":          runners.check,
		"act":            runners.act,
	})
	return coordinator, func() {
		cancel()
		coordinator.wait()
	}
}

type fakeNotifyRunner struct {
	mu      sync.Mutex
	result  string
	err     error
	started chan string
	release chan struct{}
	onRun   func()
}

func (r *fakeNotifyRunner) RunJob(ctx context.Context, _ string, task string) (string, error) {
	if r.started != nil {
		r.started <- task
	}
	if r.onRun != nil {
		r.onRun()
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
