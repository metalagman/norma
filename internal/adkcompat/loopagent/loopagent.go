package loopagent

import (
	"fmt"
	"iter"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

// Config defines a loop agent compatible with the removed ADK v1 loopagent.
type Config struct {
	AgentConfig   agent.Config
	MaxIterations uint
}

// New creates an agent that repeatedly runs its sub-agents in sequence.
func New(cfg Config) (agent.Agent, error) {
	if cfg.AgentConfig.Run != nil {
		return nil, fmt.Errorf("loopagent does not allow custom Run implementations")
	}
	impl := &loopAgent{maxIterations: cfg.MaxIterations}
	cfg.AgentConfig.Run = impl.run
	ag, err := agent.New(cfg.AgentConfig)
	if err != nil {
		return nil, fmt.Errorf("create loop agent: %w", err)
	}
	return ag, nil
}

type loopAgent struct {
	maxIterations uint
}

func (a *loopAgent) run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	count := a.maxIterations
	return func(yield func(*session.Event, error) bool) {
		for {
			shouldExit := false
			for _, subAgent := range ctx.Agent().SubAgents() {
				for event, err := range subAgent.Run(ctx) {
					if !yield(event, err) {
						return
					}
					if event != nil && event.Actions.Escalate {
						shouldExit = true
					}
				}
				if shouldExit {
					return
				}
			}
			if count > 0 {
				count--
				if count == 0 {
					return
				}
			}
		}
	}
}
