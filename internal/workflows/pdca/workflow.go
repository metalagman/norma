package pdca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/pdca/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
)

// Workflow implements the ADK-based pdca workflow.
type Workflow struct {
	cfg     config.Config
	store   *db.Store
	tracker task.Tracker
}

const actDecisionClose = "close"

// NewWorkflow builds the pdca workflow runtime.
func NewWorkflow(cfg config.Config, store *db.Store, tracker task.Tracker) *Workflow {
	return &Workflow{
		cfg:     cfg,
		store:   store,
		tracker: tracker,
	}
}

func (w *Workflow) Name() string {
	return "pdca"
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

	// Create the custom pdca iteration agent.
	loopIterationAgent, err := NewPDCAAgent(w.cfg, w.store, w.tracker, input, &stepIndex, input.BaseBranch)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("create pdca iteration agent: %w", err)
	}

	la, err := newLoopAgent(w.cfg.Budgets.MaxIterations, loopIterationAgent)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("create loop agent: %w", err)
	}

	// Setup initial state
	initialState := map[string]any{
		"iteration":  1,
		"task_state": &state,
	}
	log.Info().Str("task_id", input.TaskID).Str("run_id", input.RunID).Msg("pdca workflow: starting ADK runner")
	finalSession, err := workflows.RunADK(ctx, workflows.ADKRunInput{
		AppName:      "norma",
		UserID:       "norma-user",
		Agent:        la,
		InitialState: initialState,
		OnEvent: func(ev *session.Event) {
			if ev.Content == nil {
				return
			}
			for _, p := range ev.Content.Parts {
				log.Debug().Str("part", p.Text).Msg("ADK event part")
			}
		},
	})
	if err != nil {
		log.Error().Err(err).Msg("ADK execution error")
		return workflows.RunResult{Status: "failed"}, err
	}

	verdict, decision, finalIteration, err := parseFinalState(finalSession.State())
	if err != nil {
		return workflows.RunResult{Status: "failed"}, fmt.Errorf("parse final session state: %w", err)
	}
	status, effectiveVerdict := deriveFinalOutcome(verdict, decision)
	log.Info().
		Str("verdict", verdict).
		Str("decision", decision).
		Str("effective_verdict", effectiveVerdict).
		Msg("pdca workflow: final outcome")

	if w.store != nil {
		update := db.Update{
			CurrentStepIndex: stepIndex,
			Iteration:        finalIteration,
			Status:           status,
		}
		if effectiveVerdict != "" {
			v := effectiveVerdict
			update.Verdict = &v
		}
		event := &db.Event{
			Type:    "verdict",
			Message: fmt.Sprintf("workflow completed with status=%s verdict=%s decision=%s", status, effectiveVerdict, decision),
		}
		if err := w.store.UpdateRun(ctx, input.RunID, update, event); err != nil {
			return workflows.RunResult{}, fmt.Errorf("persist final run status: %w", err)
		}
	}

	res := workflows.RunResult{
		Status: status,
	}
	if effectiveVerdict != "" {
		res.Verdict = &effectiveVerdict
	}

	return res, nil
}

func newLoopAgent(maxIterations int, loopIterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: uint(maxIterations),
		AgentConfig: agent.Config{
			Name:        "PDCALoop",
			Description: "ADK loop agent for pdca",
			SubAgents:   []agent.Agent{loopIterationAgent},
		},
	})
}

func parseFinalState(state session.State) (string, string, int, error) {
	verdict, err := stateString(state, "verdict")
	if err != nil {
		return "", "", 0, err
	}

	decision, err := stateString(state, "decision")
	if err != nil {
		return "", "", 0, err
	}

	iteration, err := statePositiveInt(state, "iteration", 1)
	if err != nil {
		return "", "", 0, err
	}

	taskState, err := stateAny(state, "task_state")
	if err != nil {
		return "", "", 0, err
	}
	if taskState != nil {
		coerced := coerceTaskState(taskState)
		if verdict == "" && coerced.Check != nil {
			verdict = strings.TrimSpace(coerced.Check.Verdict.Status)
		}
		if decision == "" && coerced.Act != nil {
			decision = strings.TrimSpace(coerced.Act.Decision)
		}
	}

	return verdict, decision, iteration, nil
}

func deriveFinalOutcome(verdict, decision string) (status string, effectiveVerdict string) {
	effectiveVerdict = strings.ToUpper(strings.TrimSpace(verdict))
	normalizedDecision := strings.ToLower(strings.TrimSpace(decision))

	if effectiveVerdict == "" && normalizedDecision == actDecisionClose {
		effectiveVerdict = "PASS"
	}

	status = "stopped"
	switch effectiveVerdict {
	case "PASS":
		status = "passed"
	case "FAIL":
		status = "failed"
	}

	return status, effectiveVerdict
}

func stateString(state session.State, key string) (string, error) {
	value, err := stateAny(state, key)
	if err != nil {
		return "", err
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

func stateAny(state session.State, key string) (any, error) {
	value, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session state key %q: %w", key, err)
	}
	return value, nil
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
