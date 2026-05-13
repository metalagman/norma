package goalkeeper

import (
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
)

const rootAgentName = "Goalkeeper"

// New creates a Goalkeeper workflow agent from options.
func New(opts Options) (agent.Agent, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate goalkeeper options: %w", err)
	}
	validator, err := newVerdictEscalatingValidator(opts.validator)
	if err != nil {
		return nil, err
	}
	return loopagent.New(loopagent.Config{
		MaxIterations: opts.maxIterations,
		AgentConfig: agent.Config{
			Name:        rootAgentName,
			Description: "Retries a worker agent and validator agent until the validator passes one goal.",
			SubAgents:   []agent.Agent{opts.worker, validator},
		},
	})
}

func newVerdictEscalatingValidator(validator agent.Agent) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        validator.Name(),
		Description: validator.Description(),
		SubAgents:   validator.SubAgents(),
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				for ev, err := range validator.Run(ctx) {
					if err != nil {
						yield(nil, err)
						return
					}
					if ev != nil && ev.IsFinalResponse() && strings.HasPrefix(visibleText(ev), "verdict: pass") {
						ev.Actions.Escalate = true
					}
					if !yield(ev, nil) {
						return
					}
				}
			}
		},
	})
}

func visibleText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var parts []string
	for _, part := range ev.Content.Parts {
		if part != nil && !part.Thought && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
