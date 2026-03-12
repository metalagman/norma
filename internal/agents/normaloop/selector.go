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
	"google.golang.org/genai"
)

var errNoTasks = errors.New("no tasks")

var defaultBackoffSteps = []time.Duration{
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	40 * time.Second,
	60 * time.Second,
}

func (w *loopRuntime) backoffSteps() []time.Duration {
	if len(w.overrideBackoffSteps) > 0 {
		return w.overrideBackoffSteps
	}
	return defaultBackoffSteps
}

func (w *loopRuntime) newSelectorAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "Selector",
		Description: "Picks the next task from the tracker or sleeps if none found.",
		Run:         w.runSelector,
	})
}

func (w *loopRuntime) runSelector(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	l := w.logger.With().
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
		Logger()

	return func(yield func(*session.Event, error) bool) {
		if ctx.Ended() {
			return
		}

		for {
			selected, reason, err := w.selectNextTask(ctx)
			if err == nil {
				l.Info().
					Str("task_id", selected.ID).
					Str("selection_reason", reason).
					Msg("selector picked task")

				_ = ctx.Session().State().Set("selector_backoff_step", 0)

				if err := ctx.Session().State().Set("selected_task_id", selected.ID); err != nil {
					yield(nil, fmt.Errorf("set selected_task_id in session: %w", err))
					return
				}
				if err := ctx.Session().State().Set("selection_reason", reason); err != nil {
					yield(nil, fmt.Errorf("set selection_reason in session: %w", err))
					return
				}
				return
			}

			if !errors.Is(err, errNoTasks) {
				yield(nil, err)
				return
			}

			// No runnable tasks, start backoff
			steps := w.backoffSteps()
			stepVal, _ := ctx.Session().State().Get("selector_backoff_step")
			step, _ := stepVal.(int)
			if step >= len(steps) {
				step = len(steps) - 1
			}
			wait := steps[step]

			l.Info().
				Dur("wait_duration", wait).
				Int("backoff_step", step).
				Msg("no runnable tasks found, waiting with backoff")

			ev := session.NewEvent(ctx.InvocationID())
			ev.Partial = true
			ev.Content = &genai.Content{
				Parts: []*genai.Part{
					{Text: fmt.Sprintf("No runnable tasks found. Waiting %v before retrying...", wait)},
				},
			}
			if !yield(ev, nil) {
				return
			}

			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			// Increment backoff step for next iteration
			if step < len(steps)-1 {
				step++
			}
			_ = ctx.Session().State().Set("selector_backoff_step", step)
		}
	}
}

func (w *loopRuntime) selectNextTask(ctx context.Context) (task.Task, string, error) {
	items, err := w.tracker.LeafTasks(ctx)
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
