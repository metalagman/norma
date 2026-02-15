package pdca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/metalagman/norma/internal/agents/pdca/models"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/session"
)

// Factory builds and finalizes PDCA ADK agents.
type Factory struct {
	cfg     config.Config
	store   *db.Store
	tracker task.Tracker
}

const actDecisionClose = "close"

// NewFactory constructs a PDCA agent factory.
func NewFactory(cfg config.Config, store *db.Store, tracker task.Tracker) *Factory {
	return &Factory{
		cfg:     cfg,
		store:   store,
		tracker: tracker,
	}
}

func (w *Factory) Name() string {
	return "pdca"
}

func (w *Factory) Build(ctx context.Context, meta runpkg.RunMeta, task runpkg.TaskPayload) (runpkg.AgentBuild, error) {
	input := AgentInput{
		RunID:              meta.RunID,
		Goal:               task.Goal,
		AcceptanceCriteria: task.AcceptanceCriteria,
		TaskID:             task.ID,
		RunDir:             meta.RunDir,
		GitRoot:            meta.GitRoot,
		BaseBranch:         meta.BaseBranch,
	}

	stepsDir := filepath.Join(input.RunDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return runpkg.AgentBuild{}, err
	}

	taskItem, err := w.tracker.Task(ctx, input.TaskID)
	if err != nil {
		return runpkg.AgentBuild{}, err
	}

	state := models.TaskState{}
	if taskItem.Notes != "" {
		if err := json.Unmarshal([]byte(taskItem.Notes), &state); err != nil {
			return runpkg.AgentBuild{}, fmt.Errorf("parse task notes state: %w", err)
		}
	}

	stepIndex := 0

	// Create the pdca loop agent with plan/do/check/act as direct sub-agents.
	la, err := NewLoopAgent(w.cfg, w.store, w.tracker, input, &stepIndex, input.BaseBranch, w.cfg.Budgets.MaxIterations)
	if err != nil {
		return runpkg.AgentBuild{}, fmt.Errorf("create loop agent: %w", err)
	}

	// Setup initial state
	initialState := map[string]any{
		"iteration":  1,
		"task_state": &state,
	}
	log.Info().Str("task_id", input.TaskID).Str("run_id", input.RunID).Msg("pdca: built ADK loop agent")

	return runpkg.AgentBuild{
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
	}, nil
}

func (w *Factory) Finalize(ctx context.Context, meta runpkg.RunMeta, _ runpkg.TaskPayload, finalSession session.Session) (runpkg.AgentOutcome, error) {
	if finalSession == nil {
		return runpkg.AgentOutcome{}, fmt.Errorf("final session is required")
	}
	verdict, decision, finalIteration, err := parseFinalState(finalSession.State())
	if err != nil {
		return runpkg.AgentOutcome{Status: "failed"}, fmt.Errorf("parse final session state: %w", err)
	}

	stepIndex, err := stateNonNegativeInt(finalSession.State(), "current_step_index", 0)
	if err != nil {
		return runpkg.AgentOutcome{Status: "failed"}, fmt.Errorf("read final step index: %w", err)
	}

	status, effectiveVerdict := deriveFinalOutcome(verdict, decision)
	log.Info().
		Str("verdict", verdict).
		Str("decision", decision).
		Str("effective_verdict", effectiveVerdict).
		Msg("pdca agent: final outcome")

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
			Message: fmt.Sprintf("pdca agent run completed with status=%s verdict=%s decision=%s", status, effectiveVerdict, decision),
		}
		if err := w.store.UpdateRun(ctx, meta.RunID, update, event); err != nil {
			return runpkg.AgentOutcome{}, fmt.Errorf("persist final run status: %w", err)
		}
	}

	res := runpkg.AgentOutcome{
		Status: status,
	}
	if effectiveVerdict != "" {
		res.Verdict = &effectiveVerdict
	}

	return res, nil
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

func stateNonNegativeInt(state session.State, key string, defaultValue int) (int, error) {
	value, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return defaultValue, nil
		}
		return 0, fmt.Errorf("read session state key %q: %w", key, err)
	}

	parsed, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("session state key %q has type %T; want int", key, value)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("session state key %q must be >= 0; got %d", key, parsed)
	}
	return parsed, nil
}
