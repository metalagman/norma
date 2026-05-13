package goalkeeper

import (
	"fmt"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
)

const rootAgentName = "Goalkeeper"

// New creates a Goalkeeper workflow agent that runs worker and then validator.
func New(worker agent.Agent, validator agent.Agent, setters ...OptOptionsSetter) (agent.Agent, error) {
	opts := NewOptions(worker, validator, setters...)
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate goalkeeper options: %w", err)
	}
	return sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        rootAgentName,
			Description: "Runs a worker agent and then a validator agent for one goal.",
			SubAgents:   []agent.Agent{opts.worker, opts.validator},
		},
	})
}
