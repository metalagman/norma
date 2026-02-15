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

	"github.com/metalagman/norma/internal/adkrunner"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/reconcile"
	runpkg "github.com/metalagman/norma/internal/run"
)

var taskIDPattern = regexp.MustCompile(`^norma-[a-z0-9]+(?:\.[a-z0-9]+)*$`)

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
