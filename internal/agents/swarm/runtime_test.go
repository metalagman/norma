package swarm

import (
	"context"
	"testing"

	"github.com/normahq/norma/v2/internal/config"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/rs/zerolog"
)

func TestInferOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		beforeAssignee  string
		afterAssignee   string
		afterStatus     string
		primaryAssignee string
		want            taskOutcome
	}{
		{name: "completed", beforeAssignee: "norma-implementer", afterAssignee: "norma-implementer", afterStatus: "done", primaryAssignee: "norma-coordinator", want: taskOutcomeCompleted},
		{name: "handoff", beforeAssignee: "norma-implementer", afterAssignee: "norma-reviewer", afterStatus: "todo", primaryAssignee: "norma-coordinator", want: taskOutcomeHandedOff},
		{name: "unassigned", beforeAssignee: "norma-implementer", afterAssignee: "", afterStatus: "todo", primaryAssignee: "norma-coordinator", want: taskOutcomeNeedsHumanTriage},
		{name: "bounce to coordinator", beforeAssignee: "norma-implementer", afterAssignee: "norma-implementer", afterStatus: "todo", primaryAssignee: "norma-coordinator", want: taskOutcomeBouncedToCoordinator},
		{name: "coordinator no progress", beforeAssignee: "norma-coordinator", afterAssignee: "norma-coordinator", afterStatus: "todo", primaryAssignee: "norma-coordinator", want: taskOutcomeNoProgress},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := inferOutcome(
				task.Task{Assignee: tc.beforeAssignee},
				task.Task{Assignee: tc.afterAssignee, Status: tc.afterStatus},
				tc.primaryAssignee,
			)
			if err != nil {
				t.Fatalf("inferOutcome() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("inferOutcome() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectCandidates_ReportsUnassignedAndIncludesOffEpicAssignedWork(t *testing.T) {
	t.Parallel()

	tracker := &mockTracker{
		tasks: map[string]task.Task{
			"norma-epic":      {ID: "norma-epic", Type: "epic"},
			"norma-feature.1": {ID: "norma-feature.1", Type: "feature", ParentID: "norma-epic"},
			"norma-task.1":    {ID: "norma-task.1", Type: "task", ParentID: "norma-feature.1"},
			"norma-other.1":   {ID: "norma-other.1", Type: "task", ParentID: "norma-outside"},
			"norma-outside":   {ID: "norma-outside", Type: "feature"},
		},
	}

	runtime := &Runtime{
		logger:  zerolog.Nop(),
		tracker: tracker,
		roleByAssignee: map[string]*roleRuntime{
			"norma-implementer": {config: config.ResolvedSwarmRoleConfig{Assignee: "norma-implementer"}},
		},
	}

	leaves := []task.Task{
		{ID: "norma-task.1", ParentID: "norma-feature.1", Assignee: "", Priority: 2},
		{ID: "norma-other.1", ParentID: "norma-outside", Assignee: "norma-implementer", Priority: 1},
	}

	candidates, reports := runtime.selectCandidates(context.Background(), "norma-epic", leaves, map[string]string{})
	if len(reports) != 1 {
		t.Fatalf("len(reports) = %d, want 1", len(reports))
	}
	if reports[0].TaskID != "norma-task.1" {
		t.Fatalf("reports[0].TaskID = %q, want norma-task.1", reports[0].TaskID)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].task.ID != "norma-other.1" {
		t.Fatalf("candidates[0].task.ID = %q, want norma-other.1", candidates[0].task.ID)
	}
}

type mockTracker struct {
	tasks map[string]task.Task
}

func (m *mockTracker) Add(context.Context, string, string, []task.AcceptanceCriterion, *string) (string, error) {
	panic("unexpected call")
}

func (m *mockTracker) AddEpic(context.Context, string, string) (string, error) {
	panic("unexpected call")
}

func (m *mockTracker) AddFeature(context.Context, string, string, string) (string, error) {
	panic("unexpected call")
}

func (m *mockTracker) List(context.Context, *string) ([]task.Task, error) { panic("unexpected call") }
func (m *mockTracker) ListFeatures(context.Context, string) ([]task.Task, error) {
	panic("unexpected call")
}
func (m *mockTracker) Children(context.Context, string) ([]task.Task, error) {
	panic("unexpected call")
}
func (m *mockTracker) Task(_ context.Context, id string) (task.Task, error) { return m.tasks[id], nil }
func (m *mockTracker) MarkDone(context.Context, string) error               { panic("unexpected call") }
func (m *mockTracker) MarkStatus(context.Context, string, string) error     { panic("unexpected call") }
func (m *mockTracker) Update(context.Context, string, string, string) error { panic("unexpected call") }
func (m *mockTracker) Delete(context.Context, string) error                 { panic("unexpected call") }
func (m *mockTracker) SetRun(context.Context, string, string) error         { panic("unexpected call") }
func (m *mockTracker) AddDependency(context.Context, string, string) error  { panic("unexpected call") }
func (m *mockTracker) LeafTasks(context.Context) ([]task.Task, error)       { panic("unexpected call") }
func (m *mockTracker) UpdateWorkflowState(context.Context, string, string) error {
	panic("unexpected call")
}
func (m *mockTracker) AddLabel(context.Context, string, string) error    { panic("unexpected call") }
func (m *mockTracker) RemoveLabel(context.Context, string, string) error { panic("unexpected call") }
func (m *mockTracker) SetAssignee(context.Context, string, string) error { panic("unexpected call") }
func (m *mockTracker) SetNotes(context.Context, string, string) error    { panic("unexpected call") }
func (m *mockTracker) CloseWithReason(context.Context, string, string) error {
	panic("unexpected call")
}
func (m *mockTracker) AddRelatedLink(context.Context, string, string) error { panic("unexpected call") }
func (m *mockTracker) ListBlockedDependents(context.Context, string) ([]task.Task, error) {
	panic("unexpected call")
}
func (m *mockTracker) AddFollowUp(context.Context, string, string, string, []task.AcceptanceCriterion) (string, error) {
	panic("unexpected call")
}
