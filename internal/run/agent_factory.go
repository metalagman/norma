package run

import (
	"context"

	"github.com/metalagman/norma/internal/task"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

// RunMeta contains shared run metadata.
type RunMeta struct {
	RunID      string
	RunDir     string
	GitRoot    string
	BaseBranch string
}

// TaskPayload contains task-level input available to factories.
type TaskPayload struct {
	ID                 string
	Goal               string
	AcceptanceCriteria []task.AcceptanceCriterion
}

// AgentBuild describes an ADK agent build for a task run.
type AgentBuild struct {
	Agent        agent.Agent
	InitialState map[string]any
	OnEvent      func(*session.Event)
}

// AgentOutcome summarizes the run outcome.
type AgentOutcome struct {
	Status  string
	Verdict *string
}

// AgentFactory builds and finalizes ADK agents for task runs.
type AgentFactory interface {
	Name() string
	Build(ctx context.Context, meta RunMeta, task TaskPayload) (AgentBuild, error)
	Finalize(ctx context.Context, meta RunMeta, task TaskPayload, finalSession session.Session) (AgentOutcome, error)
}
