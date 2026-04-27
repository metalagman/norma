package tasksmcp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCoordinatorAssigningTracker_AssignsCreatedIssues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(context.Context, *coordinatorAssigningTracker) (string, error)
	}{
		{
			name: "task",
			run: func(ctx context.Context, tracker *coordinatorAssigningTracker) (string, error) {
				return tracker.Add(ctx, "Task", "Goal", nil, nil)
			},
		},
		{
			name: "epic",
			run: func(ctx context.Context, tracker *coordinatorAssigningTracker) (string, error) {
				return tracker.AddEpic(ctx, "Epic", "Goal")
			},
		},
		{
			name: "feature",
			run: func(ctx context.Context, tracker *coordinatorAssigningTracker) (string, error) {
				return tracker.AddFeature(ctx, "norma-epic.1", "Feature", "Goal")
			},
		},
		{
			name: "follow-up",
			run: func(ctx context.Context, tracker *coordinatorAssigningTracker) (string, error) {
				return tracker.AddFollowUp(ctx, "norma-parent.1", "Follow-up", "Goal", nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := &mockTracker{}
			tracker := NewCoordinatorAssigningTracker(base, "norma-coordinator").(*coordinatorAssigningTracker)

			id, err := tc.run(context.Background(), tracker)
			if err != nil {
				t.Fatalf("create error = %v", err)
			}
			if len(base.setAssignees) != 1 {
				t.Fatalf("len(setAssignees) = %d, want 1", len(base.setAssignees))
			}
			if got := base.setAssignees[0]; got.ID != id || got.Assignee != "norma-coordinator" {
				t.Fatalf("setAssignee call = %+v, want id=%q assignee=%q", got, id, "norma-coordinator")
			}
		})
	}
}

func TestCoordinatorAssigningTracker_AllowsExplicitLaterSetAssignee(t *testing.T) {
	t.Parallel()

	base := &mockTracker{}
	tracker := NewCoordinatorAssigningTracker(base, "norma-coordinator")

	if _, err := tracker.Add(context.Background(), "Task", "Goal", nil, nil); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := tracker.SetAssignee(context.Background(), "norma-task.1", "norma-planner"); err != nil {
		t.Fatalf("SetAssignee() error = %v", err)
	}

	if len(base.setAssignees) != 2 {
		t.Fatalf("len(setAssignees) = %d, want 2", len(base.setAssignees))
	}
	if got := base.setAssignees[1]; got.ID != "norma-task.1" || got.Assignee != "norma-planner" {
		t.Fatalf("explicit setAssignee call = %+v, want id=%q assignee=%q", got, "norma-task.1", "norma-planner")
	}
}

func TestCoordinatorAssigningTracker_ReturnsErrorWhenAssignmentFails(t *testing.T) {
	t.Parallel()

	base := &mockTracker{failByMethod: map[string]error{"SetAssignee": errors.New("boom")}}
	tracker := NewCoordinatorAssigningTracker(base, "norma-coordinator")

	_, err := tracker.Add(context.Background(), "Task", "Goal", nil, nil)
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
	if got := err.Error(); got == "boom" || !containsAll(got, []string{"assign issue", "norma-task.1", "norma-coordinator", "boom"}) {
		t.Fatalf("Add() error = %q, want wrapped assignment failure", got)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
