package normaloop

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/task"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

var errNoTasks = errors.New("no tasks")

func (w *Loop) newSelectorAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "Selector",
		Description: "Picks the next task from the tracker or sleeps if none found.",
		Run:         w.runSelector,
	})
}

func (w *Loop) runSelector(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	l := w.logger.With().
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
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
