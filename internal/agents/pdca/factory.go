package pdca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/internal/agents/pdca/contracts"
	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/internal/db"
	runpkg "github.com/normahq/norma/internal/run"
	"github.com/normahq/norma/internal/task"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Factory builds and finalizes PDCA ADK agents.
type Factory struct {
	cfg           config.Config
	maxIterations int
	store         *db.Store
	tracker       task.Tracker
}

const actDecisionClose = "close"

// NewFactory constructs a PDCA agent factory.
func NewFactory(cfg config.Config, maxIterations int, store *db.Store, tracker task.Tracker) *Factory {
	return &Factory{
		cfg:           cfg,
		maxIterations: maxIterations,
		store:         store,
		tracker:       tracker,
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
		WorkingDir:         meta.GitRoot,
		BaseBranch:         meta.BaseBranch,
	}

	stepsDir := filepath.Join(input.RunDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o700); err != nil {
		return runpkg.AgentBuild{}, err
	}

	state := contracts.TaskState{}

	// Create the pdca loop agent with plan/do/check/act as direct subagents.
	la, err := NewLoopAgent(ctx, w.cfg, w.store, w.tracker, input, input.BaseBranch, w.maxIterations)
	if err != nil {
		return runpkg.AgentBuild{}, fmt.Errorf("create loop agent: %w", err)
	}

	// Setup initial state
	initialState := map[string]any{
		"iteration":  1,
		"task_state": &state,
	}
	l := log.With().Str("component", "pdca").Logger()
	l.Info().Str("task_id", input.TaskID).Str("run_id", input.RunID).Msg("built ADK loop agent")

	return runpkg.AgentBuild{
		Agent:          la,
		SessionID:      input.TaskID,
		InitialState:   initialState,
		InitialContent: genai.NewContentFromText(input.Goal, genai.RoleUser),
		OnEvent: func(ev *session.Event) {
			if ev.Content == nil {
				return
			}
			for _, p := range ev.Content.Parts {
				l.Debug().Str("part", p.Text).Msg("ADK event part")
			}
		},
	}, nil
}

func (w *Factory) Finalize(ctx context.Context, meta runpkg.RunMeta, payload runpkg.TaskPayload, finalSession session.Session) (runpkg.AgentOutcome, error) {
	if finalSession == nil {
		return runpkg.AgentOutcome{}, fmt.Errorf("final session is required")
	}

	l := log.With().Str("component", "pdca").Logger()

	verdict, decision, finalIteration, err := parseFinalState(finalSession.State())
	if err != nil {
		return runpkg.AgentOutcome{Status: "failed"}, fmt.Errorf("parse final session state: %w", err)
	}

	stepIndex, err := stateNonNegativeInt(finalSession.State(), "current_step_index", 0)
	if err != nil {
		return runpkg.AgentOutcome{Status: "failed"}, fmt.Errorf("read final step index: %w", err)
	}

	status, effectiveVerdict := deriveFinalOutcome(verdict, decision)
	l.Info().
		Str("verdict", verdict).
		Str("decision", decision).
		Str("effective_verdict", effectiveVerdict).
		Msg("final outcome")

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
	if decision != "" {
		res.Decision = &decision
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
	if err != nil && !errors.Is(err, session.ErrStateKeyNotExist) {
		return "", "", 0, err
	}
	if err == nil {
		coerced := coerceTaskState(taskState)
		if verdict == "" && len(coerced.Check) > 0 {
			var checkOutput struct {
				Verdict string `json:"verdict"`
			}
			if err := json.Unmarshal(coerced.Check, &checkOutput); err == nil {
				verdict = strings.TrimSpace(checkOutput.Verdict)
			}
		}
		if decision == "" && len(coerced.Act) > 0 {
			var actOutput struct {
				Decision string `json:"decision"`
			}
			if err := json.Unmarshal(coerced.Act, &actOutput); err == nil {
				decision = strings.TrimSpace(actOutput.Decision)
			}
		}
	}

	return verdict, decision, iteration, nil
}

func deriveFinalOutcome(verdict, decision string) (status string, effectiveVerdict string) {
	effectiveVerdict = strings.ToUpper(strings.TrimSpace(verdict))
	normalizedDecision := strings.ToLower(strings.TrimSpace(decision))

	status = "stopped"
	switch effectiveVerdict {
	case "PASS":
		if normalizedDecision == actDecisionClose {
			status = "passed"
		}
	case "FAIL":
		status = "failed"
	}

	return status, effectiveVerdict
}

func stateString(state session.State, key string) (string, error) {
	value, err := stateAny(state, key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return "", nil
		}
		return "", err
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
