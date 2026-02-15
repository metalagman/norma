package normaloop

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/adkrunner"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/reconcile"
	runpkg "github.com/metalagman/norma/internal/run"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
)

const (
	statusDoing    = "doing"
	statusTodo     = "todo"
	statusPlanning = "planning"
)

const maxLoopIterations uint = 1_000_000

var errNoTasks = errors.New("no tasks")
var taskIDPattern = regexp.MustCompile(`^norma-[a-z0-9]+(?:\.[a-z0-9]+)*$`)

type runStatusStore interface {
	GetRunStatus(ctx context.Context, runID string) (string, error)
	CreateRun(ctx context.Context, runID, goal, runDir string, iteration int) error
	UpdateRun(ctx context.Context, runID string, update db.Update, event *db.Event) error
	DB() *sql.DB
}

// Loop orchestrates repeated task execution for `norma loop`.
type Loop struct {
	agent.Agent
	logger         zerolog.Logger
	cfg            config.Config
	repoRoot       string
	normaDir       string
	tracker        task.Tracker
	runStore       runStatusStore
	factory        runpkg.AgentFactory
	continueOnFail bool
	policy         task.SelectionPolicy
}

// NewLoop constructs the normaloop ADK loop agent runtime.
func NewLoop(logger zerolog.Logger, cfg config.Config, repoRoot string, tracker task.Tracker, runStore runStatusStore, factory runpkg.AgentFactory, continueOnFail bool, policy task.SelectionPolicy) (*Loop, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute repo root: %w", err)
	}

	w := &Loop{
		logger:         logger.With().Str("component", "normaloop").Logger(),
		cfg:            cfg,
		repoRoot:       absRoot,
		normaDir:       filepath.Join(absRoot, ".norma"),
		tracker:        tracker,
		runStore:       runStore,
		factory:        factory,
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
	if !taskIDPattern.MatchString(id) {
		return fmt.Errorf("invalid task id: %s", id)
	}

	item, err := w.tracker.Task(ctx, id)
	if err != nil {
		return err
	}

	switch item.Status {
	case statusTodo, runpkg.StatusFailed, runpkg.StatusStopped:
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
		if err := w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed); err != nil {
			return err
		}
	default:
		return fmt.Errorf("task %s status is %s", id, item.Status)
	}

	startedAt := time.Now().UTC()
	runID, err := newRunID()
	if err != nil {
		return err
	}

	w.logger.Info().Str("task_id", id).Str("run_id", runID).Msg("starting task run")

	lock, err := runpkg.AcquireRunLock(w.normaDir)
	if err != nil {
		return fmt.Errorf("acquire run lock: %w", err)
	}
	defer func() {
		if lErr := lock.Release(); lErr != nil {
			w.logger.Warn().Err(lErr).Msg("failed to release run lock")
		}
	}()

	if err := os.MkdirAll(w.normaDir, 0o755); err != nil {
		return fmt.Errorf("create .norma: %w", err)
	}

	baseBranch := ""
	if w.repoRoot != "" {
		var err error
		baseBranch, err = git.CurrentBranch(ctx, w.repoRoot)
		if err != nil {
			return fmt.Errorf("resolve base branch: %w", err)
		}
		// Prune stalled worktrees
		_ = git.RunCmdErr(ctx, w.repoRoot, "git", "worktree", "prune")
	}

	if w.runStore != nil && w.runStore.DB() != nil {
		if err := reconcile.Run(ctx, w.runStore.DB(), w.normaDir); err != nil {
			return err
		}
	}

	runDir := filepath.Join(w.normaDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}

	if w.runStore != nil {
		if err := w.runStore.CreateRun(ctx, runID, item.Goal, runDir, 1); err != nil {
			return fmt.Errorf("create run in store: %w", err)
		}
	}

	if err := w.tracker.SetRun(ctx, id, runID); err != nil {
		w.logger.Warn().Err(err).Msg("failed to set run id in tracker")
	}

	if err := w.tracker.MarkStatus(ctx, id, statusPlanning); err != nil {
		return err
	}

	meta := runpkg.RunMeta{
		RunID:      runID,
		RunDir:     runDir,
		GitRoot:    w.repoRoot,
		BaseBranch: baseBranch,
	}
	payload := runpkg.TaskPayload{
		ID:                 id,
		Goal:               item.Goal,
		AcceptanceCriteria: item.Criteria,
	}

	build, err := w.factory.Build(ctx, meta, payload)
	if err != nil {
		_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("build run agent: %w", err)
	}

	finalSession, err := adkrunner.Run(ctx, adkrunner.RunInput{
		AppName:      "norma",
		UserID:       "norma-user",
		SessionID:    build.SessionID,
		Agent:        build.Agent,
		InitialState: build.InitialState,
		OnEvent:      build.OnEvent,
	})
	if err != nil {
		_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("execute ADK agent: %w", err)
	}

	outcome, err := w.factory.Finalize(ctx, meta, payload, finalSession)
	if err != nil {
		_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("finalize run: %w", err)
	}

	if outcome.Verdict != nil && *outcome.Verdict == "PASS" {
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Msg("verdict is PASS, applying changes")
		err = w.applyChanges(ctx, runID, item.Goal, id)
		if err != nil {
			w.logger.Error().Err(err).Msg("failed to apply changes")
			_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
			return fmt.Errorf("apply changes: %w", err)
		}
		if err := w.tracker.MarkStatus(ctx, id, "done"); err != nil {
			w.logger.Warn().Err(err).Msg("failed to mark task as done in tracker")
		}
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Str("duration", time.Since(startedAt).String()).Msg("task passed")
		return nil
	}

	w.logger.Warn().Str("task_id", id).Str("run_id", runID).Str("status", outcome.Status).Msg("task did not pass")
	if outcome.Status == runpkg.StatusFailed {
		_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("task %s failed (run %s)", id, runID)
	}
	_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusStopped)
	return fmt.Errorf("task %s stopped (run %s)", id, runID)
}

func (w *Loop) applyChanges(ctx context.Context, runID, goal, taskID string) error {
	if w.repoRoot == "" {
		return nil
	}
	branchName := fmt.Sprintf("norma/task/%s", taskID)
	stepIndex, err := w.currentStepIndex(ctx, runID)
	if err != nil {
		return err
	}
	commitMsg := runpkg.BuildApplyCommitMessage(goal, runID, stepIndex, taskID)

	w.logger.Info().Str("branch", branchName).Msg("applying changes from workspace")

	dirty := strings.TrimSpace(git.RunCmd(ctx, w.repoRoot, "git", "status", "--porcelain"))
	stashed := false
	if dirty != "" {
		w.logger.Info().Msg("stashing local changes before merge")
		if err := git.RunCmdErr(ctx, w.repoRoot, "git", "stash", "push", "-u", "-m", fmt.Sprintf("norma pre-apply %s", runID)); err != nil {
			return fmt.Errorf("git stash push: %w", err)
		}
		stashed = true
	}

	restoreStash := func() error {
		if !stashed {
			return nil
		}
		if err := git.RunCmdErr(ctx, w.repoRoot, "git", "stash", "pop"); err != nil {
			return fmt.Errorf("git stash pop: %w", err)
		}
		stashed = false
		return nil
	}

	beforeHash := strings.TrimSpace(git.RunCmd(ctx, w.repoRoot, "git", "rev-parse", "HEAD"))

	if err := git.RunCmdErr(ctx, w.repoRoot, "git", "merge", "--squash", branchName); err != nil {
		_ = git.RunCmdErr(ctx, w.repoRoot, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git merge --squash: %w", err)
	}

	if err := git.RunCmdErr(ctx, w.repoRoot, "git", "add", "-A"); err != nil {
		_ = git.RunCmdErr(ctx, w.repoRoot, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git add -A: %w", err)
	}

	status := git.RunCmd(ctx, w.repoRoot, "git", "status", "--porcelain")
	if strings.TrimSpace(status) == "" {
		_ = restoreStash()
		w.logger.Info().Msg("nothing to commit after merge")
		return nil
	}

	if err := git.RunCmdErr(ctx, w.repoRoot, "git", "commit", "-m", commitMsg); err != nil {
		_ = git.RunCmdErr(ctx, w.repoRoot, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git commit: %w", err)
	}

	if err := restoreStash(); err != nil {
		return err
	}

	afterHash := strings.TrimSpace(git.RunCmd(ctx, w.repoRoot, "git", "rev-parse", "HEAD"))
	w.logger.Info().
		Str("before_hash", beforeHash).
		Str("after_hash", afterHash).
		Msg("changes applied and committed successfully")

	return nil
}

func (w *Loop) currentStepIndex(ctx context.Context, runID string) (int, error) {
	if w.runStore == nil || w.runStore.DB() == nil {
		return 0, nil
	}
	var stepIndex int
	err := w.runStore.DB().QueryRowContext(ctx, `SELECT current_step_index FROM runs WHERE run_id=?`, runID).Scan(&stepIndex)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("read current step index for run %s: %w", runID, err)
	}
	return stepIndex, nil
}

func newRunID() (string, error) {
	suffix, err := randomHex(3)
	if err != nil {
		return "", err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", ts, suffix), nil
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
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
