package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"time"

	"github.com/metalagman/ainvoke/adk"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type loopRunner struct {
	cfg  config.AgentConfig
	role models.Role
}

func newLoopRunner(cfg config.AgentConfig, role models.Role) (Runner, error) {
	return &loopRunner{
		cfg:  cfg,
		role: role,
	}, nil
}

func (r *loopRunner) Run(ctx context.Context, req models.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, _ := r.role.Prompt(req)
	out, err := RunLoop(ctx, r.cfg, req, stdout, stderr, prompt, r.role.InputSchema(), r.role.OutputSchema())
	if err != nil {
		return nil, nil, 1, err
	}
	return out, nil, 0, nil
}

// RunLoop executes a loop agent with a normalized request.
func RunLoop(ctx context.Context, cfg config.AgentConfig, req models.AgentRequest, stdout, stderr io.Writer, prompt, inputSchema, outputSchema string) ([]byte, error) {
	startTime := time.Now()
	log.Info().Str("run_id", req.Run.ID).Int("iteration", req.Run.Iteration).Int("sub_agents", len(cfg.SubAgents)).Msg("starting ADK RunLoop")

	// Save prompt to logs/prompt.txt
	if req.Paths.RunDir != "" && prompt != "" {
		promptPath := filepath.Join(req.Paths.RunDir, "logs", "prompt.txt")
		_ = os.MkdirAll(filepath.Dir(promptPath), 0o755)
		if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
			log.Warn().Err(err).Str("path", promptPath).Msg("failed to save prompt log")
		}
	}

	inputJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	// Create adk ExecAgents for sub-agents directly from config
	adkSubAgents := make([]agent.Agent, 0, len(cfg.SubAgents))
	for i, subCfg := range cfg.SubAgents {
		log.Debug().Int("index", i).Interface("cmd", subCfg.Cmd).Msg("creating sub-agent for loop")
		opts := []adk.OptExecAgentOptionsSetter{
			adk.WithExecAgentRunDir(req.Paths.RunDir),
			adk.WithExecAgentUseTTY(subCfg.UseTTY != nil && *subCfg.UseTTY),
			adk.WithExecAgentStdout(stdout),
			adk.WithExecAgentStderr(stderr),
		}
		if prompt != "" {
			opts = append(opts, adk.WithExecAgentPrompt(prompt))
		}
		if inputSchema != "" {
			opts = append(opts, adk.WithExecAgentInputSchema(inputSchema))
		}
		if outputSchema != "" {
			opts = append(opts, adk.WithExecAgentOutputSchema(outputSchema))
		}

		sub, err := adk.NewExecAgent(
			fmt.Sprintf("sub-%d", i),
			"Norma sub-agent",
			subCfg.Cmd,
			opts...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create exec agent for sub-agent %d: %w", i, err)
		}

		// Wrap to handle escalation from JSON output
		wrapped := &escalationWrapper{Agent: sub}
		adkSubAgents = append(adkSubAgents, wrapped)
	}

	// Create adk LoopAgent
	la, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(cfg.MaxIterations),
		AgentConfig: agent.Config{
			Name:        "norma_loop_agent",
			Description: "Norma Loop Agent using ADK",
			SubAgents:   adkSubAgents,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create adk loop agent: %w", err)
	}

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma",
		Agent:          la,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create adk runner: %w", err)
	}

	userID := "norma-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  userID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	userContent := genai.NewContentFromText(string(inputJSON), genai.RoleUser)
	events := adkRunner.Run(ctx, userID, sess.Session.ID(), userContent, agent.RunConfig{})

	var lastOutBytes []byte
	for ev, err := range events {
		if err != nil {
			log.Error().Err(err).Msg("ADK loop execution failed")
			return nil, fmt.Errorf("adk loop execution error: %w", err)
		}
		if ev.Content != nil && len(ev.Content.Parts) > 0 {
			lastOutBytes = []byte(ev.Content.Parts[0].Text)
		}
	}

	if len(lastOutBytes) == 0 {
		log.Warn().Msg("ADK loop produced no output")
		return nil, fmt.Errorf("no output from loop agent")
	}

	log.Info().Int64("duration_ms", time.Since(startTime).Milliseconds()).Msg("ADK RunLoop completed")

	// Parse last response to ensure it's valid and update timing
	var agentResp models.AgentResponse
	if err := json.Unmarshal(lastOutBytes, &agentResp); err != nil {
		// Fallback to raw bytes if not valid AgentResponse JSON
		return lastOutBytes, nil
	}

	if agentResp.Summary.Text != "" {
		agentResp.Summary.Text = fmt.Sprintf("[ADK Loop completed] %s", agentResp.Summary.Text)
	} else {
		agentResp.Summary.Text = "ADK Loop completed"
	}
	agentResp.Timing.WallTimeMS = time.Since(startTime).Milliseconds()

	finalOut, err := json.Marshal(agentResp)
	if err != nil {
		return lastOutBytes, nil
	}

	return finalOut, nil
}

type escalationWrapper struct {
	agent.Agent
}

func (w *escalationWrapper) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for ev, err := range w.Agent.Run(ctx) {
			if err != nil {
				yield(nil, err)
				return
			}

			// Check for escalation in JSON output
			if ev.Content != nil && len(ev.Content.Parts) > 0 {
				var resp models.AgentResponse
				if err := json.Unmarshal([]byte(ev.Content.Parts[0].Text), &resp); err == nil {
					if resp.Escalate || resp.Status == "stop" || resp.Status == "error" {
						ev.Actions.Escalate = true
					}
				}
			}

			if !yield(ev, nil) {
				return
			}
		}
	}
}
