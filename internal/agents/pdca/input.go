package pdca

import "github.com/metalagman/norma/internal/task"

// AgentInput is PDCA-specific input used to build the PDCA ADK agent.
type AgentInput struct {
	RunID              string
	Goal               string
	AcceptanceCriteria []task.AcceptanceCriterion
	TaskID             string
	RunDir             string
	GitRoot            string
	BaseBranch         string
}
