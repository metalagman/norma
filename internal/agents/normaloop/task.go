package normaloop

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/normahq/norma/internal/adkrunner"
	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/internal/reconcile"
	runpkg "github.com/normahq/norma/internal/run"
	"github.com/normahq/norma/internal/task"
)

var taskIDPattern = regexp.MustCompile(`^norma-[a-z0-9]+(?:\.[a-z0-9]+)*$`)

func (w *loopRuntime) runTaskByID(ctx context.Context, id string) error {
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
			status, err := w.runStore.RunStatus(ctx, *item.RunID)
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

	if err := os.MkdirAll(w.normaDir, 0o700); err != nil {
		return fmt.Errorf("create .norma: %w", err)
	}

	baseBranch := ""
	if w.workingDir != "" {
		var err error
		baseBranch, err = git.CurrentBranch(ctx, w.workingDir)
		if err != nil {
			return fmt.Errorf("resolve base branch: %w", err)
		}
		// Prune stalled worktrees
		_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "worktree", "prune")
	}

	if w.runStore != nil && w.runStore.DB() != nil {
		if err := reconcile.Run(ctx, w.runStore.DB(), w.normaDir); err != nil {
			return err
		}
	}

	runDir := filepath.Join(w.normaDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
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
		GitRoot:    w.workingDir,
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

	finalSession, _, err := adkrunner.Run(ctx, adkrunner.RunInput{
		AppName:        "norma",
		UserID:         "norma-user",
		SessionID:      build.SessionID,
		Agent:          build.Agent,
		InitialState:   build.InitialState,
		InitialContent: build.InitialContent,
		OnEvent:        build.OnEvent,
	})
	if err != nil {
		_ = w.tracker.MarkStatus(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("execute ADK agent: %w", err)
	}

	outcome, err := w.factory.Finalize(ctx, meta, payload, finalSession)
	if err != nil {
		_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("finalize run: %w", err)
	}

	if outcome.Verdict != nil && *outcome.Verdict == "PASS" {
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Msg("verdict is PASS, applying changes")
		err = w.applyChanges(ctx, runID, item.Goal, id)
		if err != nil {
			w.logger.Error().Err(err).Msg("failed to apply changes")
			_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusFailed)
			return fmt.Errorf("apply changes: %w", err)
		}
		if err := w.markStatusWithTimeout(ctx, id, "done"); err != nil {
			w.logger.Warn().Err(err).Msg("failed to mark task as done in tracker")
		} else {
			// Try to finalize parent feature/epic if all children are done.
			if err := w.finalizeAncestors(ctx, item.ParentID); err != nil {
				w.logger.Warn().Err(err).Str("parent_id", item.ParentID).Msg("failed to finalize ancestors")
			}
		}
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Str("duration", time.Since(startedAt).String()).Msg("task passed")
		return nil
	}

	w.logger.Warn().Str("task_id", id).Str("run_id", runID).Str("status", outcome.Status).Msg("task did not pass")

	if outcome.Decision != nil && *outcome.Decision == "replan" {
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Msg("handling replan decision")
		if err := w.handleReplan(ctx, id, item); err != nil {
			w.logger.Error().Err(err).Msg("failed to handle replan")
			_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusFailed)
			return fmt.Errorf("handle replan: %w", err)
		}
		w.logger.Info().Str("task_id", id).Str("duration", time.Since(startedAt).String()).Msg("task handled replan, returning without hard failure")
		return nil
	}
	if outcome.Decision != nil && *outcome.Decision == "rollback" {
		w.logger.Info().Str("task_id", id).Str("run_id", runID).Msg("handling rollback decision")
		if err := w.handleRollback(ctx, id, runID); err != nil {
			w.logger.Error().Err(err).Msg("failed to handle rollback")
			_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusFailed)
			return fmt.Errorf("handle rollback: %w", err)
		}
		w.logger.Info().Str("task_id", id).Str("duration", time.Since(startedAt).String()).Msg("task rollback completed, continuing loop")
		return nil
	}

	if outcome.Status == runpkg.StatusFailed {
		_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusFailed)
		return fmt.Errorf("task %s failed (run %s)", id, runID)
	}
	_ = w.markStatusWithTimeout(ctx, id, runpkg.StatusStopped)
	return fmt.Errorf("task %s stopped (run %s)", id, runID)
}

func (w *loopRuntime) markStatusWithTimeout(ctx context.Context, id, status string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return w.tracker.MarkStatus(ctx, id, status)
}

func (w *loopRuntime) finalizeAncestors(ctx context.Context, parentID string) error {
	if strings.TrimSpace(parentID) == "" {
		return nil
	}

	parent, err := w.tracker.Task(ctx, parentID)
	if err != nil {
		return fmt.Errorf("fetch parent %s: %w", parentID, err)
	}

	// Completion Rules (AGENTS.md):
	// - Feature complete: All descendant leaf issues are closed.
	// - Epic complete: All features under it are complete.
	// We only auto-close features and epics.
	if parent.Type != "feature" && parent.Type != "epic" {
		return nil
	}

	children, err := w.tracker.Children(ctx, parentID)
	if err != nil {
		return fmt.Errorf("list children for parent %s: %w", parentID, err)
	}

	allDone := true
	for _, child := range children {
		if child.Status != "done" {
			allDone = false
			break
		}
	}

	if allDone && len(children) > 0 {
		w.logger.Info().Str("id", parentID).Str("type", parent.Type).Msg("all children completed, closing parent")
		if err := w.tracker.MarkStatus(ctx, parentID, "done"); err != nil {
			return fmt.Errorf("mark parent %s as done: %w", parentID, err)
		}
		// Recurse to parent of parent
		return w.finalizeAncestors(ctx, parent.ParentID)
	}

	return nil
}

func (w *loopRuntime) applyChanges(ctx context.Context, runID, goal, taskID string) error {
	if w.workingDir == "" {
		return nil
	}
	branchName := fmt.Sprintf("norma/task/%s", taskID)
	stepIndex, err := w.currentStepIndex(ctx, runID)
	if err != nil {
		return err
	}
	commitMsg := runpkg.BuildApplyCommitMessage(goal, runID, stepIndex, taskID)

	w.logger.Info().Str("branch", branchName).Msg("applying changes from workspace")

	dirty := strings.TrimSpace(git.GitRunCmd(ctx, w.workingDir, "git", "status", "--porcelain"))
	stashed := false
	if dirty != "" {
		w.logger.Info().Msg("stashing local changes before merge")
		if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "stash", "push", "-u", "-m", fmt.Sprintf("norma pre-apply %s", runID)); err != nil {
			return fmt.Errorf("git stash push: %w", err)
		}
		stashed = true
	}

	restoreStash := func() error {
		if !stashed {
			return nil
		}
		if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "stash", "pop"); err != nil {
			return fmt.Errorf("git stash pop: %w", err)
		}
		stashed = false
		return nil
	}

	beforeHash := strings.TrimSpace(git.GitRunCmd(ctx, w.workingDir, "git", "rev-parse", "HEAD"))

	if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "merge", "--squash", branchName); err != nil {
		_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git merge --squash: %w", err)
	}

	if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "add", "-A"); err != nil {
		_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git add -A: %w", err)
	}

	status := git.GitRunCmd(ctx, w.workingDir, "git", "status", "--porcelain")
	if strings.TrimSpace(status) == "" {
		_ = restoreStash()
		w.logger.Info().Msg("nothing to commit after merge")
		return nil
	}

	if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "commit", "-m", commitMsg); err != nil {
		_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git commit: %w", err)
	}

	if err := restoreStash(); err != nil {
		return err
	}

	afterHash := strings.TrimSpace(git.GitRunCmd(ctx, w.workingDir, "git", "rev-parse", "HEAD"))
	w.logger.Info().
		Str("before_hash", beforeHash).
		Str("after_hash", afterHash).
		Msg("changes applied and committed successfully")

	return nil
}

func (w *loopRuntime) currentStepIndex(ctx context.Context, runID string) (int, error) {
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

func (w *loopRuntime) handleRollback(ctx context.Context, taskID, runID string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	branchName := fmt.Sprintf("norma/task/%s", taskID)
	if err := w.dropTaskBranch(ctx, branchName); err != nil {
		return err
	}

	if err := w.tracker.MarkStatus(ctx, taskID, statusTodo); err != nil {
		return fmt.Errorf("reset task status to todo: %w", err)
	}

	w.logger.Info().
		Str("task_id", taskID).
		Str("run_id", runID).
		Str("branch", branchName).
		Msg("rollback reset completed")
	return nil
}

func (w *loopRuntime) dropTaskBranch(ctx context.Context, branchName string) error {
	if strings.TrimSpace(w.workingDir) == "" || !git.Available(ctx, w.workingDir) {
		return nil
	}

	_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "worktree", "prune")

	worktreePaths, err := worktreePathsForBranch(ctx, w.workingDir, branchName)
	if err != nil {
		return err
	}
	for _, worktreePath := range worktreePaths {
		if filepath.Clean(worktreePath) == filepath.Clean(w.workingDir) {
			return fmt.Errorf("cannot rollback while main worktree is on branch %q", branchName)
		}
		if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "worktree", "remove", "--force", worktreePath); err != nil {
			return fmt.Errorf("remove task worktree %q: %w", worktreePath, err)
		}
	}

	_ = git.GitRunCmdErr(ctx, w.workingDir, "git", "worktree", "prune")

	exists, err := localBranchExists(ctx, w.workingDir, branchName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := git.GitRunCmdErr(ctx, w.workingDir, "git", "branch", "-D", branchName); err != nil {
		return fmt.Errorf("delete task branch %q: %w", branchName, err)
	}
	return nil
}

func localBranchExists(ctx context.Context, workingDir, branchName string) (bool, error) {
	if strings.TrimSpace(branchName) == "" {
		return false, fmt.Errorf("branch name is required")
	}
	ref := "refs/heads/" + strings.TrimSpace(branchName)
	if err := git.GitRunCmdErr(ctx, workingDir, "git", "show-ref", "--verify", "--quiet", ref); err != nil {
		return false, nil
	}
	return true, nil
}

func worktreePathsForBranch(ctx context.Context, workingDir, branchName string) ([]string, error) {
	out, err := git.GitRunCmdOutput(ctx, workingDir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list git worktrees: %w", err)
	}

	wantRef := "refs/heads/" + strings.TrimSpace(branchName)
	paths := make([]string, 0)
	currentPath := ""
	currentBranch := ""

	flush := func() {
		if currentPath != "" && currentBranch == wantRef {
			paths = append(paths, currentPath)
		}
		currentPath = ""
		currentBranch = ""
	}

	for _, rawLine := range strings.Split(out, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			currentBranch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		}
	}
	flush()
	return paths, nil
}

func (w *loopRuntime) handleReplan(ctx context.Context, oldTaskID string, oldTask task.Task) error {
	// Safety timeout to prevent infinite hangs on tracker operations
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	staleLabels := []string{"norma-has-plan", "norma-has-do", "norma-has-check"}
	for _, label := range staleLabels {
		if err := w.tracker.RemoveLabel(ctx, oldTaskID, label); err != nil {
			w.logger.Warn().Err(err).Str("label", label).Msg("failed to remove stale workflow label")
		}
	}
	w.logger.Info().Str("task_id", oldTaskID).Int("removed", len(staleLabels)).Msg("removed stale workflow labels")

	replanTitle := fmt.Sprintf("Replan: %s", oldTask.Title)
	replanGoal := fmt.Sprintf("Replan required for task %s. Original goal: %s", oldTaskID, oldTask.Goal)

	newTaskID, err := w.tracker.AddFollowUp(ctx, oldTask.ParentID, replanTitle, replanGoal, oldTask.Criteria)
	if err != nil {
		return fmt.Errorf("create replanning task: %w", err)
	}
	w.logger.Info().Str("old_task_id", oldTaskID).Str("new_task_id", newTaskID).Msg("created replanning task")

	if err := w.tracker.AddRelatedLink(ctx, oldTaskID, newTaskID); err != nil {
		w.logger.Warn().Err(err).Msg("failed to add related link between old and new task")
	}

	blockedDependents, err := w.tracker.ListBlockedDependents(ctx, oldTaskID)
	if err != nil {
		w.logger.Warn().Err(err).Msg("failed to list blocked dependents")
	} else {
		w.logger.Info().Msg("rewiring blocked dependents to new replanning task")
		for _, dep := range blockedDependents {
			if err := w.tracker.AddDependency(ctx, dep.ID, newTaskID); err != nil {
				w.logger.Warn().Err(err).Str("dep_id", dep.ID).Msg("failed to add new task as blocker")
			}
		}
		w.logger.Info().Int("rewired_count", len(blockedDependents)).Msg("rewired blocked dependents to new replanning task")
	}

	if err := w.tracker.AddLabel(ctx, oldTaskID, "replan-needed"); err != nil {
		w.logger.Warn().Err(err).Msg("failed to add replan-needed label")
	}

	if err := w.tracker.CloseWithReason(ctx, oldTaskID, "wont do: replan needed"); err != nil {
		return fmt.Errorf("close old task with reason: %w", err)
	}

	w.logger.Info().Str("old_task_id", oldTaskID).Msg("old task closed with replan reason")
	return nil
}
