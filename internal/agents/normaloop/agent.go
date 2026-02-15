package normaloop

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
)

const (
	statusFailed   = "failed"
	statusStopped  = "stopped"
	statusPassed   = "passed"
	statusDoing    = "doing"
	statusTodo     = "todo"
	statusPlanning = "planning"
)

const maxLoopIterations uint = 1_000_000

var errNoTasks = errors.New("no tasks")

type taskRunner interface {
	Run(ctx context.Context, goal string, ac []task.AcceptanceCriterion, taskID string) (runpkg.Result, error)
}

type runStatusStore interface {
	GetRunStatus(ctx context.Context, runID string) (string, error)
}

// Loop orchestrates repeated task execution for `norma loop`.
type Loop struct {
	agent.Agent
	logger         zerolog.Logger
	tracker        task.Tracker
	runStore       runStatusStore
	taskRunner     taskRunner
	continueOnFail bool
	policy         task.SelectionPolicy
}

// NewLoop constructs the normaloop ADK loop agent runtime.
func NewLoop(logger zerolog.Logger, tracker task.Tracker, runStore runStatusStore, taskRunner taskRunner, continueOnFail bool, policy task.SelectionPolicy) (*Loop, error) {
	w := &Loop{
		logger:         logger.With().Str("component", "normaloop").Logger(),
		tracker:        tracker,
		runStore:       runStore,
		taskRunner:     taskRunner,
		continueOnFail: continueOnFail,
		policy:         policy,
	}

	iterationAgent, err := w.newIterationAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop iteration agent: %w", err)
	}
	selectorAgent, err := w.newSelectorAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop selector agent: %w", err)
	}
	loopAgent, err := w.newLoopAgent(selectorAgent, iterationAgent)
	if err != nil {
		return nil, fmt.Errorf("create normaloop loop agent: %w", err)
	}

	w.Agent = loopAgent
	return w, nil
}

func (w *Loop) newSelectorAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "Selector",
		Description: "Picks the next task from the tracker or sleeps if none found.",
		Run:         w.runSelector,
	})
}

func (w *Loop) newIterationAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "NormaLoopIteration",
		Description: "Runs a single normaloop iteration.",
		Run:         w.runIteration,
	})
}

func (w *Loop) newLoopAgent(selectorAgent, iterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: maxLoopIterations,
		AgentConfig: agent.Config{
			Name:        "NormaLoopAgent",
			Description: "Reads ready tasks and runs PDCA workflow per selected task.",
			SubAgents:   []agent.Agent{selectorAgent, iterationAgent},
		},
	})
}

func (w *Loop) runSelector(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	l := w.logger.With().
		Str("agent_name", "Selector").
		Str("agent_id", ctx.InvocationID()).
		Logger()

	return func(yield func(*session.Event, error) bool) {
		if ctx.Ended() {
			return
		}

		selected, reason, err := w.selectNextTask(ctx)
		if err != nil {
			if errors.Is(err, errNoTasks) {
				l.Debug().Msg("no runnable tasks left, sleeping 10s...")
				_ = ctx.Session().State().Set("selected_task_id", "")
				time.Sleep(10 * time.Second)
				return
			}
			yield(nil, err)
			return
		}

		l.Info().
			Str("task_id", selected.ID).
			Str("selection_reason", reason).
			Msg("selector picked task")

		if err := ctx.Session().State().Set("selected_task_id", selected.ID); err != nil {
			yield(nil, fmt.Errorf("set selected_task_id in session: %w", err))
			return
		}
		if err := ctx.Session().State().Set("selection_reason", reason); err != nil {
			yield(nil, fmt.Errorf("set selection_reason in session: %w", err))
			return
		}
	}
}

func (w *Loop) runIteration(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	l := w.logger.With().
		Str("agent_name", "NormaLoopIteration").
		Str("agent_id", ctx.InvocationID()).
		Logger()

	return func(yield func(*session.Event, error) bool) {
		if ctx.Ended() {
			return
		}

		taskIDVal, err := ctx.Session().State().Get("selected_task_id")
		if err != nil {
			yield(nil, fmt.Errorf("get selected_task_id from session: %w", err))
			return
		}
		taskID, ok := taskIDVal.(string)
		if !ok || taskID == "" {
			return
		}

		iteration := 1
		if value, err := ctx.Session().State().Get("iteration"); err == nil {
			if parsed, ok := value.(int); ok && parsed > 0 {
				iteration = parsed
			}
		}

		l.Info().
			Int("iteration", iteration).
			Str("task_id", taskID).
			Msg("starting iteration")

		err = w.runTaskByID(ctx, taskID)
		if err != nil {
			if !w.continueOnFail {
				yield(nil, err)
				return
			}
			l.Error().Err(err).Str("task_id", taskID).Msg("task failed, continuing loop")
		}

		if err := ctx.Session().State().Set("iteration", iteration+1); err != nil {
			yield(nil, fmt.Errorf("set iteration in session: %w", err))
			return
		}

		// Clear the task ID so selector can pick a new one (or sleep) next time
		_ = ctx.Session().State().Set("selected_task_id", "")
	}
}

func (w *Loop) selectNextTask(ctx context.Context) (task.Task, string, error) {
	status := statusTodo
	items, err := w.tracker.List(ctx, &status)
	if err != nil {
		return task.Task{}, "", err
	}

	items = filterRunnableTasks(items)
	if len(items) == 0 {
		return task.Task{}, "", errNoTasks
	}

	selected, reason, err := task.SelectNextReady(ctx, w.tracker, items, w.policy)
	if err != nil {
		return task.Task{}, "", err
	}

	return selected, reason, nil
}

func (w *Loop) runTaskByID(ctx context.Context, id string) error {
	item, err := w.tracker.Task(ctx, id)
	if err != nil {
		return err
	}

	switch item.Status {
	case statusTodo, statusFailed, statusStopped:
	case statusDoing:
		if item.RunID != nil {
			status, err := w.runStore.GetRunStatus(ctx, *item.RunID)
			if err != nil {
				return err
			}
			if status == "running" {
				return fmt.Errorf("task %s already running", id)
			}
		}
		if err := w.tracker.MarkStatus(ctx, id, statusFailed); err != nil {
			return err
		}
	default:
		return fmt.Errorf("task %s status is %s", id, item.Status)
	}

	if err := w.tracker.MarkStatus(ctx, id, statusPlanning); err != nil {
		return err
	}

	result, err := w.taskRunner.Run(ctx, item.Goal, item.Criteria, id)
	if err != nil {
		_ = w.tracker.MarkStatus(ctx, id, statusFailed)
		return err
	}

	if result.RunID != "" {
		_ = w.tracker.SetRun(ctx, id, result.RunID)
	}

	switch result.Status {
	case statusPassed:
		w.logger.Info().Str("task_id", id).Str("run_id", result.RunID).Msg("task passed")
		return nil
	case statusFailed:
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	case statusStopped:
		return fmt.Errorf("task %s stopped (run %s)", id, result.RunID)
	default:
		return fmt.Errorf("task %s failed (run %s)", id, result.RunID)
	}
}

func filterRunnableTasks(items []task.Task) []task.Task {
	out := make([]task.Task, 0, len(items))
	for _, item := range items {
		if isRunnableTask(item) {
			out = append(out, item)
		}
	}
	return out
}

func isRunnableTask(item task.Task) bool {
	typ := strings.ToLower(strings.TrimSpace(item.Type))
	switch typ {
	case "epic", "feature":
		return false
	default:
		return true
	}
}
