package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"time"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type loopRunner struct {
	cfg       config.AgentConfig
	subAgents []Runner
	role      models.Role
}

func newLoopRunner(cfg config.AgentConfig, role models.Role) (Runner, error) {
	subAgents := make([]Runner, 0, len(cfg.SubAgents))
	for i, subCfg := range cfg.SubAgents {
		subRunner, err := NewRunner(subCfg, role)
		if err != nil {
			return nil, fmt.Errorf("init sub-agent %d: %w", i, err)
		}
		subAgents = append(subAgents, subRunner)
	}

	return &loopRunner{
		cfg:       cfg,
		subAgents: subAgents,
		role:      role,
	}, nil
}

func (r *loopRunner) Run(ctx context.Context, req models.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	startTime := time.Now()

	var lastOutBytes []byte
	var lastErrBytes []byte
	var lastExitCode int
	var lastAgentResp models.AgentResponse

	// Wrap norma Runners as adk Agents
	adkSubAgents := make([]agent.Agent, 0, len(r.subAgents))
	for i, sub := range r.subAgents {
		subIdx := i
		subRunner := sub
		a, err := agent.New(agent.Config{
			Name:        fmt.Sprintf("sub-%d", subIdx),
			Description: "Norma sub-agent",
			Run: func(invCtx agent.InvocationContext) iter.Seq2[*session.Event, error] {
				return func(yield func(*session.Event, error) bool) {
					outBytes, errBytes, exitCode, err := subRunner.Run(invCtx, req, stdout, stderr)
					lastOutBytes = outBytes
					lastErrBytes = errBytes
					lastExitCode = exitCode

					if err != nil {
						yield(nil, err)
						return
					}

					var agentResp models.AgentResponse
					if err := json.Unmarshal(outBytes, &agentResp); err == nil {
						lastAgentResp = agentResp
						event := &session.Event{
							Actions: session.EventActions{
								Escalate: agentResp.Escalate,
							},
						}
						if agentResp.Status == "stop" || agentResp.Status == "error" {
							event.Actions.Escalate = true
						}
						yield(event, nil)
					} else {
						yield(&session.Event{}, nil)
					}
				}
			},
		})
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to create adk agent for sub-agent %d: %w", i, err)
		}
		adkSubAgents = append(adkSubAgents, a)
	}

	// Create adk LoopAgent
	la, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(r.cfg.MaxIterations),
		AgentConfig: agent.Config{
			Name:        "norma_loop_agent",
			Description: "Norma Loop Agent using ADK",
			SubAgents:   adkSubAgents,
		},
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create adk loop agent: %w", err)
	}

	// Run the loop agent using a minimal InvocationContext
	invCtx := &normaInvocationContext{Context: ctx}
	for _, err := range la.Run(invCtx) {
		if err != nil {
			return lastOutBytes, lastErrBytes, lastExitCode, fmt.Errorf("adk loop execution error: %w", err)
		}
	}

	// Final summary update
	if lastAgentResp.Summary.Text != "" {
		lastAgentResp.Summary.Text = fmt.Sprintf("[ADK Loop completed] %s", lastAgentResp.Summary.Text)
	} else {
		lastAgentResp.Summary.Text = "ADK Loop completed"
	}
	lastAgentResp.Timing.WallTimeMS = time.Since(startTime).Milliseconds()

	finalOut, err := json.Marshal(lastAgentResp)
	if err != nil {
		return lastOutBytes, lastErrBytes, lastExitCode, nil
	}

	return finalOut, lastErrBytes, lastExitCode, nil
}

type normaInvocationContext struct {
	context.Context
}

func (m *normaInvocationContext) Agent() agent.Agent             { return nil }
func (m *normaInvocationContext) Artifacts() agent.Artifacts     { return nil }
func (m *normaInvocationContext) Memory() agent.Memory           { return nil }
func (m *normaInvocationContext) Session() session.Session       { return nil }
func (m *normaInvocationContext) InvocationID() string           { return "norma-inv-1" }
func (m *normaInvocationContext) Branch() string                 { return "main" }
func (m *normaInvocationContext) UserContent() *genai.Content    { return nil }
func (m *normaInvocationContext) RunConfig() *agent.RunConfig    { return nil }
func (m *normaInvocationContext) EndInvocation()                 {}
func (m *normaInvocationContext) Ended() bool                   { return false }