package normaloop

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/metalagman/norma/internal/db"
	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
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

// Workflow orchestrates repeated task execution for `norma loop`.
type Workflow struct {
	tracker        task.Tracker
	runStore       runStatusStore
	taskRunner     taskRunner
	continueOnFail bool
	policy         task.SelectionPolicy
}

// NewWorkflow constructs the normaloop ADK workflow.
func NewWorkflow(tracker task.Tracker, runStore *db.Store, taskRunner *runpkg.Runner, continueOnFail bool, policy task.SelectionPolicy) *Workflow {
	return &Workflow{
		tracker:        tracker,
		runStore:       runStore,
		taskRunner:     taskRunner,
		continueOnFail: continueOnFail,
		policy:         policy,
	}
}

// Run executes the normaloop ADK agent until stop conditions are met.
func (w *Workflow) Run(ctx context.Context) error {
	iterationAgent, err := w.newIterationAgent()
	if err != nil {
		return fmt.Errorf("create normaloop iteration agent: %w", err)
	}
	loopAgent, err := w.newLoopAgent(iterationAgent)
	if err != nil {
		return fmt.Errorf("create normaloop loop agent: %w", err)
	}

	sessionService := session.InMemoryService()
	adkRunner, err := adkrunner.New(adkrunner.Config{
		AppName:        "norma",
		Agent:          loopAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return fmt.Errorf("create ADK runner: %w", err)
	}

	userID := "norma-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma",
		UserID:  userID,
		State: map[string]any{
			"iteration": 1,
		},
	})
	if err != nil {
		return fmt.Errorf("create ADK session: %w", err)
	}

	input := genai.NewContentFromText("Run norma task loop", genai.RoleUser)
	events := adkRunner.Run(ctx, userID, sess.Session.ID(), input, agent.RunConfig{})
	for _, runErr := range events {
		if runErr != nil {
			return runErr
		}
	}

	return nil
}

func (w *Workflow) newIterationAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "NormaLoopIteration",
		Description: "Runs a single normaloop iteration.",
		Run:         w.runIteration,
	})
}

func (w *Workflow) newLoopAgent(iterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: maxLoopIterations,
		AgentConfig: agent.Config{
			Name:        "NormaLoopAgent",
			Description: "Reads ready tasks and runs PDCA workflow per selected task.",
			SubAgents:   []agent.Agent{iterationAgent},
		},
	})
}

func (w *Workflow) runIteration(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if ctx.Ended() {
			return
		}

		iteration := 1
		if value, err := ctx.Session().State().Get("iteration"); err == nil {
			if parsed, ok := value.(int); ok && parsed > 0 {
				iteration = parsed
			}
		}

		selected, reason, err := w.selectNextTask(ctx)
		if err != nil {
			if errors.Is(err, errNoTasks) {
				log.Info().Msg("normaloop: no runnable tasks left, stopping loop")
				_ = ctx.Session().State().Set("stop", true)
				ctx.EndInvocation()
				return
			}
			yield(nil, err)
			return
		}

		log.Info().
			Int("iteration", iteration).
			Str("task_id", selected.ID).
			Str("selection_reason", reason).
			Msg("normaloop: selected task")

		if err := ctx.Session().State().Set("selected_task_id", selected.ID); err != nil {
			yield(nil, fmt.Errorf("set selected_task_id in session: %w", err))
			return
		}
		if err := ctx.Session().State().Set("selection_reason", reason); err != nil {
			yield(nil, fmt.Errorf("set selection_reason in session: %w", err))
			return
		}

		err = w.runTaskByID(ctx, selected.ID)
		if err != nil {
			if !w.continueOnFail {
				yield(nil, err)
				return
			}
			log.Error().Err(err).Str("task_id", selected.ID).Msg("normaloop: task failed, continuing loop")
		}

		if err := ctx.Session().State().Set("iteration", iteration+1); err != nil {
			yield(nil, fmt.Errorf("set iteration in session: %w", err))
			return
		}
	}
}

func (w *Workflow) selectNextTask(ctx context.Context) (task.Task, string, error) {
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

func (w *Workflow) runTaskByID(ctx context.Context, id string) error {
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
		log.Info().Str("task_id", id).Str("run_id", result.RunID).Msg("normaloop: task passed")
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
