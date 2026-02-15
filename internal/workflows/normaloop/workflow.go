package normaloop

import (
	"context"
	"encoding/json"
	"errors"
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

// Workflow implements the ADK-based normaloop workflow.
type Workflow struct {
	cfg     config.Config
	store   *db.Store
	tracker task.Tracker
}

// NewWorkflow builds the normaloop workflow runtime.
func NewWorkflow(cfg config.Config, store *db.Store, tracker task.Tracker) *Workflow {
	return &Workflow{
		cfg:     cfg,
		store:   store,
		tracker: tracker,
	}
}

func (w *Workflow) Name() string {
	return "normaloop"
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
		if err := json.Unmarshal([]byte(taskItem.Notes), &state); err != nil {
			return workflows.RunResult{}, fmt.Errorf("parse task notes state: %w", err)
		}
	}

	stepIndex := 0

	sessionService := session.InMemoryService()

	// Create the custom normaloop iteration agent.
	loopIterationAgent, err := NewNormaLoopAgent(w.cfg, w.store, w.tracker, input, &stepIndex, input.BaseBranch)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("create normaloop iteration agent: %w", err)
	}

	la, err := newLoopAgent(w.cfg.Budgets.MaxIterations, loopIterationAgent)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("create loop agent: %w", err)
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
	genaiInput := genai.NewContentFromText(fmt.Sprintf("Run normaloop workflow for task %s: %s", input.TaskID, input.Goal), genai.RoleUser)
	log.Info().Str("task_id", input.TaskID).Str("run_id", input.RunID).Msg("normaloop workflow: starting ADK runner")
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
	log.Debug().Msg("normaloop workflow: retrieving final session state")
	finalSess, err := sessionService.Get(ctx, &session.GetRequest{
		AppName:   "norma",
		UserID:    userID,
		SessionID: sess.Session.ID(),
	})
	if err != nil {
		log.Error().Err(err).Msg("normaloop workflow: failed to retrieve final session state")
		return workflows.RunResult{Status: "failed"}, err
	}

	verdict, finalIteration, err := parseFinalState(finalSess.Session.State())
	if err != nil {
		return workflows.RunResult{Status: "failed"}, fmt.Errorf("parse final session state: %w", err)
	}
	log.Info().Str("verdict", verdict).Msg("normaloop workflow: final verdict")

	status := "stopped"
	switch verdict {
	case "PASS":
		status = "passed"
	case "FAIL":
		status = "failed"
	}

	if w.store != nil {
		update := db.Update{
			CurrentStepIndex: stepIndex,
			Iteration:        finalIteration,
			Status:           status,
		}
		if verdict != "" {
			v := verdict
			update.Verdict = &v
		}
		event := &db.Event{
			Type:    "verdict",
			Message: fmt.Sprintf("workflow completed with status=%s verdict=%s", status, verdict),
		}
		if err := w.store.UpdateRun(ctx, input.RunID, update, event); err != nil {
			return workflows.RunResult{}, fmt.Errorf("persist final run status: %w", err)
		}
	}

	res := workflows.RunResult{
		Status: status,
	}
	if verdict != "" {
		res.Verdict = &verdict
	}

	return res, nil
}

func newLoopAgent(maxIterations int, loopIterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: uint(maxIterations),
		AgentConfig: agent.Config{
			Name:        "NormaLoop",
			Description: "ADK loop agent for normaloop",
			SubAgents:   []agent.Agent{loopIterationAgent},
		},
	})
}

func parseFinalState(state session.State) (string, int, error) {
	verdict, err := stateString(state, "verdict")
	if err != nil {
		return "", 0, err
	}

	iteration, err := statePositiveInt(state, "iteration", 1)
	if err != nil {
		return "", 0, err
	}

	return verdict, iteration, nil
}

func stateString(state session.State, key string) (string, error) {
	value, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read session state key %q: %w", key, err)
	}
	if value == nil {
		return "", nil
	}

	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("session state key %q has type %T; want string", key, value)
	}
	return str, nil
}

func statePositiveInt(state session.State, key string, defaultValue int) (int, error) {
	value, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return defaultValue, nil
		}
		return 0, fmt.Errorf("read session state key %q: %w", key, err)
	}

	iteration, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("session state key %q has type %T; want int", key, value)
	}
	if iteration <= 0 {
		return 0, fmt.Errorf("session state key %q must be > 0; got %d", key, iteration)
	}
	return iteration, nil
}
