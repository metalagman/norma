package normaloop

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"runtime/debug"
	"strings"
	"time"

	"github.com/normahq/norma/v2/internal/task"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
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
		defer func() {
			if recovered := recover(); recovered != nil {
				l.Error().
					Interface("panic", recovered).
					Bytes("stack", debug.Stack()).
					Msg("selector panic recovered")
				w.waitWithSelectorBackoff(ctx, yield, "selector panic recovered")
			}
		}()

		if ctx.Ended() {
			return
		}

		for {
			selected, reason, err := w.selectNextTaskForSession(ctx, ctx.Session().State(), time.Now())
			if err == nil {
				l.Info().
					Str("task_id", selected.ID).
					Str("selection_reason", reason).
					Msg("selector picked task")

				_ = ctx.Session().State().Set(selectorBackoffStepKey, 0)

				if err := ctx.Session().State().Set("selected_task_id", selected.ID); err != nil {
					w.waitWithSelectorBackoff(ctx, yield, fmt.Sprintf("set selected_task_id in session: %v", err))
					return
				}
				if err := ctx.Session().State().Set("selection_reason", reason); err != nil {
					w.waitWithSelectorBackoff(ctx, yield, fmt.Sprintf("set selection_reason in session: %v", err))
					return
				}
				return
			}

			if errors.Is(err, errNoTasks) {
				if !w.waitWithSelectorBackoff(ctx, yield, "no runnable tasks found") {
					return
				}
				continue
			}

			w.logger.Error().Err(err).Msg("selector runtime error")
			if !w.waitWithSelectorBackoff(ctx, yield, fmt.Sprintf("selector error: %v", err)) {
				return
			}
		}
	}
}

func (w *loopRuntime) waitWithSelectorBackoff(ctx agent.InvocationContext, yield func(*session.Event, error) bool, reason string) bool {
	steps := w.backoffSteps()
	if len(steps) == 0 {
		return true
	}

	stepVal, _ := ctx.Session().State().Get(selectorBackoffStepKey)
	step, _ := stepVal.(int)
	if step < 0 {
		step = 0
	}
	if step >= len(steps) {
		step = len(steps) - 1
	}
	wait := steps[step]

	w.logger.Info().
		Dur("wait_duration", wait).
		Int("backoff_step", step).
		Str("reason", reason).
		Msg("selector waiting with backoff")

	ev := session.NewEvent(context.Background(), ctx.InvocationID())
	ev.Partial = true
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: fmt.Sprintf("%s. Waiting %v before retrying...", reason, wait)},
		},
	}
	if !yield(ev, nil) {
		return false
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}

	if step < len(steps)-1 {
		step++
	}
	_ = ctx.Session().State().Set(selectorBackoffStepKey, step)
	return true
}

func (w *loopRuntime) selectNextTask(ctx context.Context) (task.Task, string, error) {
	return w.selectNextTaskFrom(ctx, nil, time.Time{})
}

func (w *loopRuntime) selectNextTaskForSession(ctx context.Context, state session.State, now time.Time) (task.Task, string, error) {
	return w.selectNextTaskFrom(ctx, state, now)
}

func (w *loopRuntime) selectNextTaskFrom(ctx context.Context, state session.State, now time.Time) (task.Task, string, error) {
	items, err := w.tracker.LeafTasks(ctx)
	if err != nil {
		return task.Task{}, "", err
	}

	items = filterRunnableTasks(items)
	if state != nil {
		items = retryEligibleTasks(state, items, now)
	}
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
