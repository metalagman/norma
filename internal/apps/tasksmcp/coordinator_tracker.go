package tasksmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/normahq/norma/internal/task"
)

type coordinatorAssigningTracker struct {
	base                task.Tracker
	coordinatorAssignee string
}

// NewCoordinatorAssigningTracker wraps a tracker so newly created issues are
// automatically assigned to the configured coordinator assignee.
func NewCoordinatorAssigningTracker(base task.Tracker, coordinatorAssignee string) task.Tracker {
	return &coordinatorAssigningTracker{
		base:                base,
		coordinatorAssignee: strings.TrimSpace(coordinatorAssignee),
	}
}

func (t *coordinatorAssigningTracker) Add(ctx context.Context, title, goal string, criteria []task.AcceptanceCriterion, runID *string) (string, error) {
	id, err := t.base.Add(ctx, title, goal, criteria, runID)
	if err != nil {
		return "", err
	}
	return id, t.assign(ctx, id)
}

func (t *coordinatorAssigningTracker) AddEpic(ctx context.Context, title, goal string) (string, error) {
	id, err := t.base.AddEpic(ctx, title, goal)
	if err != nil {
		return "", err
	}
	return id, t.assign(ctx, id)
}

func (t *coordinatorAssigningTracker) AddFeature(ctx context.Context, epicID, title, goal string) (string, error) {
	id, err := t.base.AddFeature(ctx, epicID, title, goal)
	if err != nil {
		return "", err
	}
	return id, t.assign(ctx, id)
}

func (t *coordinatorAssigningTracker) AddFollowUp(ctx context.Context, parentID, title, goal string, criteria []task.AcceptanceCriterion) (string, error) {
	id, err := t.base.AddFollowUp(ctx, parentID, title, goal, criteria)
	if err != nil {
		return "", err
	}
	return id, t.assign(ctx, id)
}

func (t *coordinatorAssigningTracker) assign(ctx context.Context, id string) error {
	if strings.TrimSpace(t.coordinatorAssignee) == "" {
		return fmt.Errorf("coordinator assignee is required")
	}
	if err := t.base.SetAssignee(ctx, id, t.coordinatorAssignee); err != nil {
		return fmt.Errorf("assign issue %q to coordinator %q: %w", id, t.coordinatorAssignee, err)
	}
	return nil
}

func (t *coordinatorAssigningTracker) List(ctx context.Context, status *string) ([]task.Task, error) {
	return t.base.List(ctx, status)
}

func (t *coordinatorAssigningTracker) ListFeatures(ctx context.Context, epicID string) ([]task.Task, error) {
	return t.base.ListFeatures(ctx, epicID)
}

func (t *coordinatorAssigningTracker) Children(ctx context.Context, parentID string) ([]task.Task, error) {
	return t.base.Children(ctx, parentID)
}

func (t *coordinatorAssigningTracker) Task(ctx context.Context, id string) (task.Task, error) {
	return t.base.Task(ctx, id)
}

func (t *coordinatorAssigningTracker) MarkDone(ctx context.Context, id string) error {
	return t.base.MarkDone(ctx, id)
}

func (t *coordinatorAssigningTracker) MarkStatus(ctx context.Context, id, status string) error {
	return t.base.MarkStatus(ctx, id, status)
}

func (t *coordinatorAssigningTracker) Update(ctx context.Context, id, title, goal string) error {
	return t.base.Update(ctx, id, title, goal)
}

func (t *coordinatorAssigningTracker) Delete(ctx context.Context, id string) error {
	return t.base.Delete(ctx, id)
}

func (t *coordinatorAssigningTracker) SetRun(ctx context.Context, id, runID string) error {
	return t.base.SetRun(ctx, id, runID)
}

func (t *coordinatorAssigningTracker) SetAssignee(ctx context.Context, id, assignee string) error {
	return t.base.SetAssignee(ctx, id, assignee)
}

func (t *coordinatorAssigningTracker) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	return t.base.AddDependency(ctx, taskID, dependsOnID)
}

func (t *coordinatorAssigningTracker) LeafTasks(ctx context.Context) ([]task.Task, error) {
	return t.base.LeafTasks(ctx)
}

func (t *coordinatorAssigningTracker) UpdateWorkflowState(ctx context.Context, id, state string) error {
	return t.base.UpdateWorkflowState(ctx, id, state)
}

func (t *coordinatorAssigningTracker) AddLabel(ctx context.Context, id, label string) error {
	return t.base.AddLabel(ctx, id, label)
}

func (t *coordinatorAssigningTracker) RemoveLabel(ctx context.Context, id, label string) error {
	return t.base.RemoveLabel(ctx, id, label)
}

func (t *coordinatorAssigningTracker) SetNotes(ctx context.Context, id, notes string) error {
	return t.base.SetNotes(ctx, id, notes)
}

func (t *coordinatorAssigningTracker) CloseWithReason(ctx context.Context, id, reason string) error {
	return t.base.CloseWithReason(ctx, id, reason)
}

func (t *coordinatorAssigningTracker) AddRelatedLink(ctx context.Context, fromID, toID string) error {
	return t.base.AddRelatedLink(ctx, fromID, toID)
}

func (t *coordinatorAssigningTracker) ListBlockedDependents(ctx context.Context, id string) ([]task.Task, error) {
	return t.base.ListBlockedDependents(ctx, id)
}
