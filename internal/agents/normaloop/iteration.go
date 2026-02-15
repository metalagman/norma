package normaloop

import (
	"fmt"
	"iter"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

func (w *Loop) newIterationAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "NormaLoopIteration",
		Description: "Runs a single normaloop iteration.",
		Run:         w.runIteration,
	})
}

func (w *Loop) runIteration(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	l := w.logger.With().
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
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
