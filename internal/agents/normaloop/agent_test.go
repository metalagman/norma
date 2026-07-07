package normaloop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/normahq/norma/internal/db"
	runpkg "github.com/normahq/norma/internal/run"
	"github.com/normahq/norma/internal/task"
	"github.com/rs/zerolog"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

type mockTracker struct {
	mu sync.RWMutex

	listTasks []task.Task
	leafTasks []task.Task
	tasksByID map[string]task.Task
	children  map[string][]task.Task

	listErr       error
	leafErr       error
	taskErr       error
	markStatusErr error
	setRunErr     error

	markStatusCalls []string
	setRunCalls     []string

	closeWithReasonCalls       []struct{ id, reason string }
	addRelatedLinkCalls        []struct{ from, to string }
	listBlockedDependentsCalls []string
	listBlockedDependentsResp  []task.Task
	addFollowUpCalls           []struct{ parentID, title, goal string }
	addFollowUpResp            string
	addLabelCalls              []struct{ id, label string }
	removeLabelCalls           []struct{ id, label string }
}

func (m *mockTracker) Add(context.Context, string, string, []task.AcceptanceCriterion, *string) (string, error) {
	return "", nil
}
func (m *mockTracker) AddEpic(context.Context, string, string) (string, error) { return "", nil }
func (m *mockTracker) AddFeature(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (m *mockTracker) List(_ context.Context, _ *string) ([]task.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.listErr != nil {
		return nil, m.listErr
	}
	return slices.Clone(m.listTasks), nil
}
func (m *mockTracker) ListFeatures(context.Context, string) ([]task.Task, error) { return nil, nil }
func (m *mockTracker) Children(_ context.Context, parentID string) ([]task.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Clone(m.children[parentID]), nil
}
func (m *mockTracker) Task(_ context.Context, id string) (task.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.taskErr != nil {
		return task.Task{}, m.taskErr
	}
	item, ok := m.tasksByID[id]
	if !ok {
		return task.Task{}, fmt.Errorf("task %s not found", id)
	}
	return item, nil
}
func (m *mockTracker) MarkDone(context.Context, string) error { return nil }
func (m *mockTracker) MarkStatus(_ context.Context, _ string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.markStatusCalls = append(m.markStatusCalls, status)
	return m.markStatusErr
}
func (m *mockTracker) Update(context.Context, string, string, string) error { return nil }
func (m *mockTracker) Delete(context.Context, string) error                 { return nil }
func (m *mockTracker) SetRun(_ context.Context, _ string, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.setRunCalls = append(m.setRunCalls, runID)
	return m.setRunErr
}
func (m *mockTracker) SetAssignee(context.Context, string, string) error   { return nil }
func (m *mockTracker) AddDependency(context.Context, string, string) error { return nil }
func (m *mockTracker) LeafTasks(_ context.Context) ([]task.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.leafErr != nil {
		return nil, m.leafErr
	}
	return slices.Clone(m.leafTasks), nil
}
func (m *mockTracker) setLeafState(leafErr error, leafTasks []task.Task) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leafErr = leafErr
	m.leafTasks = slices.Clone(leafTasks)
}
func (m *mockTracker) UpdateWorkflowState(context.Context, string, string) error {
	return nil
}
func (m *mockTracker) AddLabel(_ context.Context, id string, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addLabelCalls = append(m.addLabelCalls, struct{ id, label string }{id, label})
	return nil
}
func (m *mockTracker) RemoveLabel(_ context.Context, id string, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLabelCalls = append(m.removeLabelCalls, struct{ id, label string }{id, label})
	return nil
}
func (m *mockTracker) SetNotes(context.Context, string, string) error { return nil }
func (m *mockTracker) CloseWithReason(_ context.Context, id string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeWithReasonCalls = append(m.closeWithReasonCalls, struct{ id, reason string }{id, reason})
	return nil
}
func (m *mockTracker) AddRelatedLink(_ context.Context, from string, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addRelatedLinkCalls = append(m.addRelatedLinkCalls, struct{ from, to string }{from, to})
	return nil
}
func (m *mockTracker) ListBlockedDependents(_ context.Context, id string) ([]task.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listBlockedDependentsCalls = append(m.listBlockedDependentsCalls, id)
	return m.listBlockedDependentsResp, nil
}
func (m *mockTracker) AddFollowUp(_ context.Context, parentID string, title string, goal string, _ []task.AcceptanceCriterion) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addFollowUpCalls = append(m.addFollowUpCalls, struct{ parentID, title, goal string }{parentID, title, goal})
	return m.addFollowUpResp, nil
}

type mockRunStore struct {
	statusByRunID map[string]string
	err           error
}

func (m *mockRunStore) RunStatus(_ context.Context, runID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.statusByRunID[runID], nil
}
func (m *mockRunStore) CreateRun(context.Context, string, string, string, int) error  { return nil }
func (m *mockRunStore) UpdateRun(context.Context, string, db.Update, *db.Event) error { return nil }
func (m *mockRunStore) DB() *sql.DB                                                   { return nil }

type mockFactory struct {
	outcome runpkg.AgentOutcome
	err     error
}

func (m *mockFactory) Name() string { return "mock" }
func (m *mockFactory) Build(context.Context, runpkg.RunMeta, runpkg.TaskPayload) (runpkg.AgentBuild, error) {
	if m.err != nil {
		return runpkg.AgentBuild{}, m.err
	}
	ag, _ := agent.New(agent.Config{
		Name: "mock",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				// dummy run
			}
		},
	})
	return runpkg.AgentBuild{Agent: ag}, nil
}
func (m *mockFactory) Finalize(context.Context, runpkg.RunMeta, runpkg.TaskPayload, session.Session) (runpkg.AgentOutcome, error) {
	return m.outcome, m.err
}

func TestIsRunnableTask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{name: "task", typ: "task", want: true},
		{name: "bug", typ: "bug", want: true},
		{name: "epic", typ: "epic", want: false},
		{name: "feature", typ: "feature", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isRunnableTask(task.Task{Type: tc.typ})
			if got != tc.want {
				t.Fatalf("isRunnableTask(%q) = %t, want %t", tc.typ, got, tc.want)
			}
		})
	}
}

func TestSelectNextTaskNoRunnableTasks(t *testing.T) {
	t.Parallel()

	tracker := &mockTracker{
		leafTasks: []task.Task{
			{ID: "norma-epic", Type: "epic"},
			{ID: "norma-feature", Type: "feature"},
		},
	}
	w := &loopRuntime{logger: zerolog.Nop(), tracker: tracker}

	_, _, err := w.selectNextTask(context.Background())
	if !errors.Is(err, errNoTasks) {
		t.Fatalf("selectNextTask() error = %v, want %v", err, errNoTasks)
	}
}

func TestRunTaskByIDPass(t *testing.T) {
	t.Parallel()

	taskID := "norma-1"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusTodo,
				Goal:   "test goal",
			},
		},
	}
	tmp := t.TempDir()
	v := verdictPass
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "", // skip git
		normaDir:   tmp,
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory: &mockFactory{
			outcome: runpkg.AgentOutcome{Status: "passed", Verdict: &v},
		},
	}

	if err := w.runTaskByID(context.Background(), taskID); err != nil {
		t.Fatalf("runTaskByID() error = %v", err)
	}

	wantCalls := []string{statusPlanning, "done"}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
	if len(tracker.setRunCalls) != 0 {
		t.Fatalf("set run calls = %v, want no calls", tracker.setRunCalls)
	}
}

func TestRunTaskByIDRunnerErrorMarksFailed(t *testing.T) {
	t.Parallel()

	taskID := "norma-2"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusTodo,
				Goal:   "test goal",
			},
		},
	}
	tmp := t.TempDir()
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "", // skip git
		normaDir:   tmp,
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory: &mockFactory{
			err: errors.New("runner failed"),
		},
	}

	err := w.runTaskByID(context.Background(), taskID)
	if err == nil {
		t.Fatal("runTaskByID() error = nil, want error")
	}

	wantCalls := []string{statusPlanning, runpkg.StatusFailed}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}

func TestRunTaskByIDStaleWorkflowStateResetsTodo(t *testing.T) {
	t.Parallel()

	taskID := "norma-stale1"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusDoing,
				Goal:   "test goal",
				Labels: []string{statusChecking},
			},
		},
	}
	tmp := t.TempDir()
	v := verdictPass
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "",
		normaDir:   tmp,
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory: &mockFactory{
			outcome: runpkg.AgentOutcome{Status: "passed", Verdict: &v},
		},
	}

	if err := w.runTaskByID(context.Background(), taskID); err != nil {
		t.Fatalf("runTaskByID() error = %v", err)
	}

	wantCalls := []string{statusTodo, statusPlanning, "done"}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}

type mockInvocationContext struct {
	agent.InvocationContext
	ctx     context.Context
	session session.Session
	agent   agent.Agent
}

func (m *mockInvocationContext) Context() context.Context { return m.ctx }
func (m *mockInvocationContext) Deadline() (time.Time, bool) {
	return m.ctx.Deadline()
}
func (m *mockInvocationContext) Done() <-chan struct{}    { return m.ctx.Done() }
func (m *mockInvocationContext) Err() error               { return m.ctx.Err() }
func (m *mockInvocationContext) Value(key any) any        { return m.ctx.Value(key) }
func (m *mockInvocationContext) Session() session.Session { return m.session }
func (m *mockInvocationContext) Agent() agent.Agent       { return m.agent }
func (m *mockInvocationContext) InvocationID() string     { return "test-id" }
func (m *mockInvocationContext) Ended() bool              { return m.ctx.Err() != nil }

func TestRunSelectorBackoff(t *testing.T) {
	t.Parallel()

	tracker := &mockTracker{}
	w := &loopRuntime{
		logger:               zerolog.Nop(),
		tracker:              tracker,
		overrideBackoffSteps: []time.Duration{time.Millisecond, 2 * time.Millisecond},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	ag, _ := w.newSelectorAgent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mctx := &mockInvocationContext{
		ctx:     ctx,
		session: sess.Session,
		agent:   ag,
	}

	// First run: no tasks, should wait and increment backoff step
	tracker.setLeafState(errNoTasks, nil)

	// We run it in a goroutine because it loops forever
	go func() {
		for range w.runSelector(mctx) {
			// consume events
		}
	}()

	// Wait a bit for the first attempt and backoff increment
	time.Sleep(100 * time.Millisecond)

	stepVal, _ := sess.Session.State().Get("selector_backoff_step")
	step, _ := stepVal.(int)
	if step == 0 {
		t.Errorf("expected backoff step > 0, got 0")
	}

	// Now add a task and see if it selects and resets backoff
	tracker.setLeafState(nil, []task.Task{{ID: "norma-1", Type: "task"}})

	// Wait for the next iteration in the loop
	time.Sleep(100 * time.Millisecond)

	selectedID, _ := sess.Session.State().Get("selected_task_id")
	if selectedID != "norma-1" {
		t.Errorf("selected task ID = %v, want norma-1", selectedID)
	}

	stepVal, _ = sess.Session.State().Get("selector_backoff_step")
	step, _ = stepVal.(int)
	if step != 0 {
		t.Errorf("expected backoff step reset to 0, got %d", step)
	}
}

func TestRunSelectorSkipsTaskUntilRetryBackoffExpires(t *testing.T) {
	t.Parallel()

	taskA := task.Task{ID: "norma-a", Type: "task", Goal: "Objective: a\nArtifact: a\nVerify: a", Priority: 0}
	taskB := task.Task{ID: "norma-b", Type: "task", Goal: "Objective: b\nArtifact: b\nVerify: b", Priority: 1}
	tracker := &mockTracker{
		leafTasks: []task.Task{taskA, taskB},
		children:  map[string][]task.Task{},
	}
	w := &loopRuntime{logger: zerolog.Nop(), tracker: tracker}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	now := time.Now()
	scheduleTaskRetry(sess.Session.State(), taskA.ID, []time.Duration{time.Hour}, now)

	selected, _, err := w.selectNextTaskForSession(context.Background(), sess.Session.State(), now)
	if err != nil {
		t.Fatalf("selectNextTaskForSession() error = %v", err)
	}
	if selected.ID != taskB.ID {
		t.Fatalf("selected ID = %q, want %q", selected.ID, taskB.ID)
	}

	selected, _, err = w.selectNextTaskForSession(context.Background(), sess.Session.State(), now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("selectNextTaskForSession() after deadline error = %v", err)
	}
	if selected.ID != taskA.ID {
		t.Fatalf("selected ID after deadline = %q, want %q", selected.ID, taskA.ID)
	}
}

func TestRunIterationTaskErrorSchedulesRetryWithoutYieldingError(t *testing.T) {
	t.Parallel()

	taskID := "norma-iterfail"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusTodo,
				Goal:   "test goal",
			},
		},
	}
	w := &loopRuntime{
		logger:               zerolog.Nop(),
		workingDir:           "",
		normaDir:             t.TempDir(),
		tracker:              tracker,
		runStore:             &mockRunStore{statusByRunID: map[string]string{}},
		factory:              &mockFactory{err: errors.New("runner failed")},
		overrideBackoffSteps: []time.Duration{time.Hour},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
		State: map[string]any{
			"selected_task_id": taskID,
			"iteration":        1,
		},
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	ag, _ := w.newIterationAgent()
	mctx := &mockInvocationContext{
		ctx:     context.Background(),
		session: sess.Session,
		agent:   ag,
	}

	for _, err := range w.runIteration(mctx) {
		if err != nil {
			t.Fatalf("runIteration yielded error = %v, want none", err)
		}
	}

	selectedID, _ := sess.Session.State().Get("selected_task_id")
	if selectedID != "" {
		t.Fatalf("selected_task_id = %v, want empty", selectedID)
	}
	iteration, _ := sess.Session.State().Get("iteration")
	if iteration != 2 {
		t.Fatalf("iteration = %v, want 2", iteration)
	}
	if _, ok := taskRetryUntil(sess.Session.State(), taskID); !ok {
		t.Fatalf("task retry deadline missing for %s", taskID)
	}

	wantCalls := []string{statusPlanning, runpkg.StatusFailed}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}

func TestRunIterationSuccessClearsTaskRetry(t *testing.T) {
	t.Parallel()

	taskID := "norma-iterpass"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusTodo,
				Goal:   "test goal",
			},
		},
	}
	v := verdictPass
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "",
		normaDir:   t.TempDir(),
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory:    &mockFactory{outcome: runpkg.AgentOutcome{Status: runpkg.StatusPassed, Verdict: &v}},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
		State: map[string]any{
			"selected_task_id": taskID,
			"iteration":        1,
		},
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}
	scheduleTaskRetry(sess.Session.State(), taskID, []time.Duration{time.Hour}, time.Now())

	ag, _ := w.newIterationAgent()
	mctx := &mockInvocationContext{
		ctx:     context.Background(),
		session: sess.Session,
		agent:   ag,
	}

	for _, err := range w.runIteration(mctx) {
		if err != nil {
			t.Fatalf("runIteration yielded error = %v, want none", err)
		}
	}

	if _, ok := taskRetryUntil(sess.Session.State(), taskID); ok {
		t.Fatalf("task retry deadline still present for %s; mark status calls = %v", taskID, tracker.markStatusCalls)
	}
}

func TestRunIterationPanicClearsSelectedTaskWithoutYieldingError(t *testing.T) {
	t.Parallel()

	taskID := "norma-panic"
	w := &loopRuntime{
		logger:               zerolog.Nop(),
		tracker:              nil,
		overrideBackoffSteps: []time.Duration{time.Hour},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
		State: map[string]any{
			"selected_task_id": taskID,
			"iteration":        1,
		},
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	ag, _ := w.newIterationAgent()
	mctx := &mockInvocationContext{
		ctx:     context.Background(),
		session: sess.Session,
		agent:   ag,
	}

	for _, err := range w.runIteration(mctx) {
		if err != nil {
			t.Fatalf("runIteration yielded error = %v, want none", err)
		}
	}

	selectedID, _ := sess.Session.State().Get("selected_task_id")
	if selectedID != "" {
		t.Fatalf("selected_task_id = %v, want empty", selectedID)
	}
	if _, ok := taskRetryUntil(sess.Session.State(), taskID); !ok {
		t.Fatalf("task retry deadline missing for %s", taskID)
	}
}

func TestRunSelectorTrackerErrorBacksOffWithoutYieldingError(t *testing.T) {
	t.Parallel()

	tracker := &mockTracker{leafErr: errors.New("tracker down")}
	w := &loopRuntime{
		logger:               zerolog.Nop(),
		tracker:              tracker,
		overrideBackoffSteps: []time.Duration{time.Millisecond},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	ag, _ := w.newSelectorAgent()
	mctx := &mockInvocationContext{
		ctx:     context.Background(),
		session: sess.Session,
		agent:   ag,
	}

	events := 0
	for _, err := range w.runSelector(mctx) {
		if err != nil {
			t.Fatalf("runSelector yielded error = %v, want none", err)
		}
		events++
		break
	}
	if events == 0 {
		t.Fatalf("runSelector yielded no backoff event")
	}
}

func TestRunSelectorPanicBacksOffWithoutYieldingError(t *testing.T) {
	t.Parallel()

	w := &loopRuntime{
		logger:               zerolog.Nop(),
		tracker:              nil,
		overrideBackoffSteps: []time.Duration{time.Millisecond},
	}

	sessionService := session.InMemoryService()
	sess, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "test",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("sessionService.Create() error = %v", err)
	}

	ag, _ := w.newSelectorAgent()
	mctx := &mockInvocationContext{
		ctx:     context.Background(),
		session: sess.Session,
		agent:   ag,
	}

	events := 0
	for _, err := range w.runSelector(mctx) {
		if err != nil {
			t.Fatalf("runSelector yielded error = %v, want none", err)
		}
		events++
		break
	}
	if events == 0 {
		t.Fatalf("runSelector yielded no backoff event")
	}
}

func TestRunTaskByIDReplanDecision(t *testing.T) {
	t.Parallel()

	taskID := "norma-replan1"
	replanDecision := "replan"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:       taskID,
				Status:   statusTodo,
				Goal:     "test goal",
				ParentID: "norma-parent",
			},
		},
		addFollowUpResp: "norma-replan1-new",
	}
	tmp := t.TempDir()
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "",
		normaDir:   tmp,
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory: &mockFactory{
			outcome: runpkg.AgentOutcome{Status: runpkg.StatusStopped, Decision: &replanDecision},
		},
	}

	err := w.runTaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("runTaskByID() error = %v", err)
	}

	wantCalls := []string{statusPlanning}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}

	if len(tracker.addFollowUpCalls) != 1 {
		t.Fatalf("addFollowUpCalls = %d, want 1", len(tracker.addFollowUpCalls))
	}
	if tracker.addFollowUpCalls[0].parentID != "norma-parent" {
		t.Errorf("addFollowUp parentID = %v, want norma-parent", tracker.addFollowUpCalls[0].parentID)
	}
	if len(tracker.addRelatedLinkCalls) != 1 {
		t.Fatalf("addRelatedLinkCalls = %d, want 1", len(tracker.addRelatedLinkCalls))
	}
	if tracker.addRelatedLinkCalls[0].from != taskID {
		t.Errorf("addRelatedLink from = %v, want %s", tracker.addRelatedLinkCalls[0].from, taskID)
	}
	if len(tracker.closeWithReasonCalls) != 1 {
		t.Fatalf("closeWithReasonCalls = %d, want 1", len(tracker.closeWithReasonCalls))
	}
	if tracker.closeWithReasonCalls[0].id != taskID {
		t.Errorf("closeWithReason id = %v, want %s", tracker.closeWithReasonCalls[0].id, taskID)
	}
}

func TestRunTaskByIDRollbackDecision(t *testing.T) {
	t.Parallel()

	taskID := "norma-rollback1"
	rollbackDecision := "rollback"
	verdict := "FAIL"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:     taskID,
				Status: statusTodo,
				Goal:   "test goal",
			},
		},
	}
	tmp := t.TempDir()
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: "",
		normaDir:   tmp,
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		factory: &mockFactory{
			outcome: runpkg.AgentOutcome{
				Status:   runpkg.StatusFailed,
				Verdict:  &verdict,
				Decision: &rollbackDecision,
			},
		},
	}

	err := w.runTaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("runTaskByID() error = %v", err)
	}

	wantCalls := []string{statusPlanning, statusTodo}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}

func TestHandleRollbackResetsTodoAndDeletesTaskBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := t.TempDir()
	initGitRepo(t, ctx, repoDir)

	taskID := "norma-rollback2"
	branchName := "norma/task/" + taskID
	runGit(t, ctx, repoDir, "checkout", "-b", branchName)
	runGit(t, ctx, repoDir, "checkout", "-")

	tracker := &mockTracker{}
	w := &loopRuntime{
		logger:     zerolog.Nop(),
		workingDir: repoDir,
		tracker:    tracker,
	}

	if err := w.handleRollback(ctx, taskID, "run-1"); err != nil {
		t.Fatalf("handleRollback() error = %v", err)
	}

	if got := strings.TrimSpace(runGitOutput(t, ctx, repoDir, "branch", "--list", branchName)); got != "" {
		t.Fatalf("branch %q still exists after rollback reset: %q", branchName, got)
	}

	wantCalls := []string{statusTodo}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}

func TestHandleReplanDoesNotTouchSkipLabels(t *testing.T) {
	t.Parallel()

	taskID := "norma-stale-1"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:       taskID,
				Status:   statusTodo,
				Goal:     "test goal",
				Title:    "Test Task",
				ParentID: "norma-parent",
			},
		},
		addFollowUpResp: "norma-stale-1-replan",
	}
	w := &loopRuntime{
		logger:  zerolog.Nop(),
		tracker: tracker,
	}

	task, err := tracker.Task(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Task() error = %v", err)
	}

	err = w.handleReplan(context.Background(), taskID, task)
	if err != nil {
		t.Fatalf("handleReplan() error = %v", err)
	}

	if len(tracker.removeLabelCalls) != 0 {
		t.Fatalf("removeLabelCalls = %v, want none", tracker.removeLabelCalls)
	}
}

func TestHandleReplanWiresBlockedDependents(t *testing.T) {
	t.Parallel()

	oldTaskID := "norma-old-1"
	newTaskID := "norma-new-1"
	blockedTaskID := "norma-blocked-1"

	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			oldTaskID: {
				ID:       oldTaskID,
				Status:   statusTodo,
				Goal:     "test goal",
				Title:    "Old Task",
				ParentID: "norma-parent",
			},
			blockedTaskID: {
				ID:     blockedTaskID,
				Status: statusTodo,
				Goal:   "blocked task",
			},
		},
		addFollowUpResp:           newTaskID,
		listBlockedDependentsResp: []task.Task{{ID: blockedTaskID}},
	}
	w := &loopRuntime{
		logger:  zerolog.Nop(),
		tracker: tracker,
	}

	task, err := tracker.Task(context.Background(), oldTaskID)
	if err != nil {
		t.Fatalf("Task() error = %v", err)
	}

	err = w.handleReplan(context.Background(), oldTaskID, task)
	if err != nil {
		t.Fatalf("handleReplan() error = %v", err)
	}

	if len(tracker.listBlockedDependentsCalls) != 1 {
		t.Fatalf("listBlockedDependentsCalls = %d, want 1", len(tracker.listBlockedDependentsCalls))
	}
	if tracker.listBlockedDependentsCalls[0] != oldTaskID {
		t.Errorf("listBlockedDependentsCalls[0] = %v, want %s", tracker.listBlockedDependentsCalls[0], oldTaskID)
	}
}

func TestHandleReplanDoesNotAddReplanLabel(t *testing.T) {
	t.Parallel()

	taskID := "norma-replan-label-1"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:       taskID,
				Status:   statusTodo,
				Goal:     "test goal",
				Title:    "Test Task",
				ParentID: "norma-parent",
			},
		},
		addFollowUpResp: "norma-replan-label-1-new",
	}
	w := &loopRuntime{
		logger:  zerolog.Nop(),
		tracker: tracker,
	}

	task, err := tracker.Task(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Task() error = %v", err)
	}

	err = w.handleReplan(context.Background(), taskID, task)
	if err != nil {
		t.Fatalf("handleReplan() error = %v", err)
	}

	for _, call := range tracker.addLabelCalls {
		if call.id == taskID && call.label == "replan-needed" {
			t.Fatalf("unexpected replan-needed label added to task %s", taskID)
		}
	}
}

func TestHandleReplanClosesWithReason(t *testing.T) {
	t.Parallel()

	taskID := "norma-close-1"
	tracker := &mockTracker{
		tasksByID: map[string]task.Task{
			taskID: {
				ID:       taskID,
				Status:   statusTodo,
				Goal:     "test goal",
				Title:    "Test Task",
				ParentID: "norma-parent",
			},
		},
		addFollowUpResp: "norma-close-1-new",
	}
	w := &loopRuntime{
		logger:  zerolog.Nop(),
		tracker: tracker,
	}

	task, err := tracker.Task(context.Background(), taskID)
	if err != nil {
		t.Fatalf("Task() error = %v", err)
	}

	err = w.handleReplan(context.Background(), taskID, task)
	if err != nil {
		t.Fatalf("handleReplan() error = %v", err)
	}

	if len(tracker.closeWithReasonCalls) != 1 {
		t.Fatalf("closeWithReasonCalls = %d, want 1", len(tracker.closeWithReasonCalls))
	}
	if tracker.closeWithReasonCalls[0].id != taskID {
		t.Errorf("closeWithReason id = %v, want %s", tracker.closeWithReasonCalls[0].id, taskID)
	}
	if tracker.closeWithReasonCalls[0].reason != "wont do: replan needed" {
		t.Errorf("closeWithReason reason = %v, want 'wont do: replan needed'", tracker.closeWithReasonCalls[0].reason)
	}
}

func initGitRepo(t *testing.T, ctx context.Context, dir string) {
	t.Helper()

	runGit(t, ctx, dir, "init")
	runGit(t, ctx, dir, "config", "user.email", "test@example.com")
	runGit(t, ctx, dir, "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runGit(t, ctx, dir, "add", "README.md")
	runGit(t, ctx, dir, "commit", "-m", "init")
}

func runGit(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func runGitOutput(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}
