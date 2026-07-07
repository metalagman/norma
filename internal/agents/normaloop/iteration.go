package normaloop

import (
	"iter"
	"runtime/debug"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

func (w *loopRuntime) newIterationAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "Iteration",
		Description: "Runs a single normaloop iteration.",
		Run:         w.runIteration,
	})
}

func (w *loopRuntime) runIteration(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
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
					Msg("iteration panic recovered")
				if taskIDVal, err := ctx.Session().State().Get("selected_task_id"); err == nil {
					if taskID, ok := taskIDVal.(string); ok && taskID != "" {
						scheduleTaskRetry(ctx.Session().State(), taskID, w.backoffSteps(), time.Now())
					}
				}
				if err := ctx.Session().State().Set("selected_task_id", ""); err != nil {
					l.Warn().Err(err).Msg("failed to clear selected task after panic")
				}
			}
		}()

		if ctx.Ended() {
			return
		}

		taskIDVal, err := ctx.Session().State().Get("selected_task_id")
		if err != nil {
			l.Error().Err(err).Msg("failed to get selected_task_id, continuing loop")
			return
		}
		taskID, ok := taskIDVal.(string)
		if !ok || taskID == "" {
			if !ok {
				l.Error().Interface("selected_task_id", taskIDVal).Msg("selected_task_id is not a string, clearing")
				_ = ctx.Session().State().Set("selected_task_id", "")
			}
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
			l.Error().Err(err).Str("task_id", taskID).Msg("task failed, scheduling retry and continuing loop")
			scheduleTaskRetry(ctx.Session().State(), taskID, w.backoffSteps(), time.Now())
		} else {
			clearTaskRetry(ctx.Session().State(), taskID)
		}

		if err := ctx.Session().State().Set("iteration", iteration+1); err != nil {
			l.Error().Err(err).Msg("failed to set iteration, continuing loop")
			return
		}

		// Clear the task ID so selector can pick a new one (or sleep) next time
		if err := ctx.Session().State().Set("selected_task_id", ""); err != nil {
			l.Error().Err(err).Msg("failed to clear selected task, continuing loop")
		}
	}
}
