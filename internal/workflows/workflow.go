package workflows

import (
	"context"

	"github.com/metalagman/norma/internal/task"
)

// Workflow defines the interface for a task execution workflow.
type Workflow interface {
	Name() string
	Run(ctx context.Context, input RunInput) (RunResult, error)
}

// RunInput contains the parameters for starting a workflow run.
type RunInput struct {
	RunID              string
	Goal               string
	AcceptanceCriteria []task.AcceptanceCriterion
	TaskID             string
	RunDir             string
	RepoRoot           string
	BaseBranch         string
}

// RunResult summarizes the outcome of a workflow run.
type RunResult struct {
	Status  string
	Verdict *string
}
