package normaloop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
)

type mockTracker struct {
	listTasks []task.Task
	tasksByID map[string]task.Task
	children  map[string][]task.Task

	listErr       error
	taskErr       error
	markStatusErr error
	setRunErr     error

	markStatusCalls []string
	setRunCalls     []string
}

func (m *mockTracker) Add(context.Context, string, string, []task.AcceptanceCriterion, *string) (string, error) {
	return "", nil
}
func (m *mockTracker) AddEpic(context.Context, string, string) (string, error) { return "", nil }
func (m *mockTracker) AddFeature(context.Context, string, string) (string, error) {
	return "", nil
}
func (m *mockTracker) List(_ context.Context, _ *string) ([]task.Task, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return slices.Clone(m.listTasks), nil
}
func (m *mockTracker) ListFeatures(context.Context, string) ([]task.Task, error) { return nil, nil }
func (m *mockTracker) Children(_ context.Context, parentID string) ([]task.Task, error) {
	return slices.Clone(m.children[parentID]), nil
}
func (m *mockTracker) Task(_ context.Context, id string) (task.Task, error) {
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
	m.markStatusCalls = append(m.markStatusCalls, status)
	return m.markStatusErr
}
func (m *mockTracker) Update(context.Context, string, string, string) error { return nil }
func (m *mockTracker) Delete(context.Context, string) error                 { return nil }
func (m *mockTracker) SetRun(_ context.Context, _ string, runID string) error {
	m.setRunCalls = append(m.setRunCalls, runID)
	return m.setRunErr
}
func (m *mockTracker) AddDependency(context.Context, string, string) error { return nil }
func (m *mockTracker) LeafTasks(context.Context) ([]task.Task, error)      { return nil, nil }
func (m *mockTracker) UpdateWorkflowState(context.Context, string, string) error {
	return nil
}
func (m *mockTracker) AddLabel(context.Context, string, string) error    { return nil }
func (m *mockTracker) RemoveLabel(context.Context, string, string) error { return nil }
func (m *mockTracker) SetNotes(context.Context, string, string) error    { return nil }

type mockRunStore struct {
	statusByRunID map[string]string
	err           error
}

func (m *mockRunStore) GetRunStatus(_ context.Context, runID string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.statusByRunID[runID], nil
}

type mockTaskRunner struct {
	result runpkg.Result
	err    error
}

func (m *mockTaskRunner) Run(context.Context, string, []task.AcceptanceCriterion, string) (runpkg.Result, error) {
	if m.err != nil {
		return runpkg.Result{}, m.err
	}
	return m.result, nil
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
		listTasks: []task.Task{
			{ID: "norma-epic", Type: "epic"},
			{ID: "norma-feature", Type: "feature"},
		},
	}
	w := &Workflow{tracker: tracker}

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
	runner := &mockTaskRunner{
		result: runpkg.Result{
			RunID:  "run-1",
			Status: statusPassed,
		},
	}
	w := &Workflow{
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		taskRunner: runner,
	}

	if err := w.runTaskByID(context.Background(), taskID); err != nil {
		t.Fatalf("runTaskByID() error = %v", err)
	}

	if len(tracker.markStatusCalls) != 1 || tracker.markStatusCalls[0] != statusPlanning {
		t.Fatalf("mark status calls = %v, want [%q]", tracker.markStatusCalls, statusPlanning)
	}
	if len(tracker.setRunCalls) != 1 || tracker.setRunCalls[0] != "run-1" {
		t.Fatalf("set run calls = %v, want [run-1]", tracker.setRunCalls)
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
	runner := &mockTaskRunner{err: errors.New("runner failed")}
	w := &Workflow{
		tracker:    tracker,
		runStore:   &mockRunStore{statusByRunID: map[string]string{}},
		taskRunner: runner,
	}

	err := w.runTaskByID(context.Background(), taskID)
	if err == nil {
		t.Fatal("runTaskByID() error = nil, want error")
	}

	wantCalls := []string{statusPlanning, statusFailed}
	if !slices.Equal(tracker.markStatusCalls, wantCalls) {
		t.Fatalf("mark status calls = %v, want %v", tracker.markStatusCalls, wantCalls)
	}
}
