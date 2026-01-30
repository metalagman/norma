package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"
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
	maxIterations := r.cfg.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 1
	}

	var lastOutBytes []byte
	var lastErrBytes []byte
	var lastExitCode int
	var lastAgentResp models.AgentResponse

	for i := 0; i < maxIterations; i++ {
		log.Debug().
			Str("role", req.Step.Name).
			Int("loop_iteration", i+1).
			Int("max_iterations", maxIterations).
			Msg("executing loop agent iteration")

		for j, subRunner := range r.subAgents {
			log.Debug().
				Str("role", req.Step.Name).
				Int("loop_iteration", i+1).
				Int("sub_agent_index", j).
				Msg("executing sub-agent")

			outBytes, errBytes, exitCode, err := subRunner.Run(ctx, req, stdout, stderr)
			lastOutBytes = outBytes
			lastErrBytes = errBytes
			lastExitCode = exitCode

			if err != nil {
				return outBytes, errBytes, exitCode, fmt.Errorf("sub-agent %d failed in iteration %d: %w", j, i+1, err)
			}

			// Parse response to check for escalation
			var agentResp models.AgentResponse
			if err := json.Unmarshal(outBytes, &agentResp); err == nil {
				lastAgentResp = agentResp
				if agentResp.Escalate {
					log.Info().
						Str("role", req.Step.Name).
						Int("loop_iteration", i+1).
						Int("sub_agent_index", j).
						Msg("sub-agent signaled escalation, stopping loop")
					return outBytes, errBytes, exitCode, nil
				}
				if agentResp.Status == "stop" || agentResp.Status == "error" {
					return outBytes, errBytes, exitCode, nil
				}
			}
		}
	}

	// If we finished all iterations without escalation, return the last response
	// but maybe wrap it in a summary that indicates we finished the loop.
	if lastAgentResp.Summary.Text != "" {
		lastAgentResp.Summary.Text = fmt.Sprintf("[Loop completed %d iterations] %s", maxIterations, lastAgentResp.Summary.Text)
	} else {
		lastAgentResp.Summary.Text = fmt.Sprintf("Loop completed %d iterations", maxIterations)
	}
	lastAgentResp.Timing.WallTimeMS = time.Since(startTime).Milliseconds()

	finalOut, err := json.Marshal(lastAgentResp)
	if err != nil {
		return lastOutBytes, lastErrBytes, lastExitCode, nil
	}

	return finalOut, lastErrBytes, lastExitCode, nil
}
