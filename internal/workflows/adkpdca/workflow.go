package adkpdca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Workflow implements the ADK-based PDCA loop.
type Workflow struct {
	cfg     config.Config
	store   *db.Store
	tracker task.Tracker
}

func NewWorkflow(cfg config.Config, store *db.Store, tracker task.Tracker) *Workflow {
	return &Workflow{
		cfg:     cfg,
		store:   store,
		tracker: tracker,
	}
}

func (w *Workflow) Name() string {
	return "adkpdca"
}

func (w *Workflow) Run(ctx context.Context, input workflows.RunInput) (workflows.RunResult, error) {
	stepsDir := filepath.Join(input.RunDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return workflows.RunResult{}, err
	}

	taskItem, err := w.tracker.Task(ctx, input.TaskID)
	if err != nil {
		return workflows.RunResult{}, err
	}

	state := models.TaskState{}
	if taskItem.Notes != "" {
		_ = json.Unmarshal([]byte(taskItem.Notes), &state)
	}

	stepIndex := 0

	sessionService := session.InMemoryService()

	// Create the custom PDCA agent
	pdcaAgent, pdcaSubAgents, err := NewNormaPDCAAgent(w.cfg, w.store, w.tracker, input, &stepIndex, input.BaseBranch, sessionService)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("failed to create custom PDCA agent: %w", err)
	}

	// Create the ADK LoopAgent. We register the orchestrator and all its sub-agents
	// as direct children of the loop agent. This ensures they are all recognized
	// by the root runner while avoiding the "multiple parents" tree error.
	allSubAgents := make([]agent.Agent, 0, 1+len(pdcaSubAgents))
	allSubAgents = append(allSubAgents, pdcaAgent)
	allSubAgents = append(allSubAgents, pdcaSubAgents...)

	la, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(w.cfg.Budgets.MaxIterations),
		AgentConfig: agent.Config{
			Name:        "NormaLoop",
			Description: "ADK Loop Agent for Norma PDCA",
			SubAgents:   allSubAgents,
		},
	})
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("failed to create loop agent: %w", err)
	}

	// Create an ADK Runner to execute the loop
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma",
		Agent:          la,
		SessionService: sessionService,
	})
	if err != nil {
		return workflows.RunResult{}, err
	}

	// Setup initial state
	initialState := map[string]any{
		"iteration":  1,
		"task_state": &state,
	}
	userID := "norma-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  userID,
		State:   initialState,
	})
	if err != nil {
		return workflows.RunResult{}, err
	}

		// Run it
		genaiInput := genai.NewContentFromText(fmt.Sprintf("Run PDCA loop for task %s: %s", input.TaskID, input.Goal), genai.RoleUser)
		log.Info().Str("task_id", input.TaskID).Str("run_id", input.RunID).Msg("ADK PDCA Workflow: starting ADK runner")
		events := adkRunner.Run(ctx, userID, sess.Session.ID(), genaiInput, agent.RunConfig{})
	
		for ev, err := range events {
			if err != nil {
				log.Error().Err(err).Msg("ADK execution error")
				return workflows.RunResult{Status: "failed"}, err
			}
			if ev.Content != nil {
				for _, p := range ev.Content.Parts {
					log.Debug().Str("part", p.Text).Msg("ADK event part")
				}
			}
		}
	
		// Retrieve final state from session
		log.Debug().Msg("ADK PDCA Workflow: retrieving final session state")
		finalSess, err := sessionService.Get(ctx, &session.GetRequest{
			AppName:   "norma",
			UserID:    userID,
			SessionID: sess.Session.ID(),
		})
		if err != nil {
			log.Error().Err(err).Msg("ADK PDCA Workflow: failed to retrieve final session state")
			return workflows.RunResult{Status: "failed"}, err
		}
	
		verdict, _ := finalSess.Session.State().Get("verdict")
		vStr, _ := verdict.(string)
		log.Info().Str("verdict", vStr).Msg("ADK PDCA Workflow: final verdict")
		
		status := "stopped"
		switch vStr {
		case "PASS":
			status = "passed"
		case "FAIL":
			status = "failed"
		}
		res := workflows.RunResult{
		Status: status,
	}
	if vStr != "" {
		res.Verdict = &vStr
	}

	return res, nil
}
