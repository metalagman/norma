package task

import (
	"context"
)

// AcceptanceCriterion describes a single acceptance criterion for a task.
type AcceptanceCriterion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	VerifyHints []string `json:"verify_hints,omitempty"`
}

// Task describes a task record.
type Task struct {
	ID        string
	Type      string // task, epic, feature
	ParentID  string
	Title     string
	Goal      string
	Criteria  []AcceptanceCriterion
	Status    string
	RunID     *string
	Priority  int
	Assignee  string
	Labels    []string
	Notes     string
	CreatedAt string
	UpdatedAt string
}

// Tracker defines the interface for task management.
type Tracker interface {
	Add(ctx context.Context, title, goal string, criteria []AcceptanceCriterion, runID *string) (string, error)
	AddEpic(ctx context.Context, title, goal string) (string, error)
	AddFeature(ctx context.Context, epicID, title string) (string, error)
	List(ctx context.Context, status *string) ([]Task, error)
	ListFeatures(ctx context.Context, epicID string) ([]Task, error)
	Children(ctx context.Context, parentID string) ([]Task, error)
	Task(ctx context.Context, id string) (Task, error)
	MarkDone(ctx context.Context, id string) error
	MarkStatus(ctx context.Context, id string, status string) error
	Update(ctx context.Context, id string, title, goal string) error
	Delete(ctx context.Context, id string) error
	SetRun(ctx context.Context, id string, runID string) error
	AddDependency(ctx context.Context, taskID, dependsOnID string) error
	LeafTasks(ctx context.Context) ([]Task, error)
	UpdateWorkflowState(ctx context.Context, id string, state string) error
	AddLabel(ctx context.Context, id string, label string) error
	RemoveLabel(ctx context.Context, id string, label string) error
	SetNotes(ctx context.Context, id string, notes string) error
}
