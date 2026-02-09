// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/reconcile"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/rs/zerolog/log"
)

const (
	statusError   = "error"
	statusFailed  = "failed"
	statusPassed  = "passed"
	statusStopped = "stopped"
)

// Runner executes a workflow for a task.
type Runner struct {
	repoRoot string
	normaDir string
	cfg      config.Config
	store    *db.Store
	tracker  task.Tracker
	workflow workflows.Workflow
}

// Result summarizes a completed run.
type Result struct {
	RunID  string
	Status string
}

// NewADKRunner constructs a Runner with an ADK-based PDCA workflow.
func NewADKRunner(repoRoot string, cfg config.Config, store *db.Store, tracker task.Tracker, wf workflows.Workflow) (*Runner, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute repo root: %w", err)
	}

	return &Runner{
		repoRoot: absRoot,
		normaDir: filepath.Join(absRoot, ".norma"),
		cfg:      cfg,
		store:    store,
		tracker:  tracker,
		workflow: wf,
	}, nil
}

func (r *Runner) validateTaskID(id string) bool {
	matched, _ := regexp.MatchString(`^norma-[a-z0-9]+$`, id)
	return matched
}

// Run starts a new run with the given goal and acceptance criteria.
func (r *Runner) Run(ctx context.Context, goal string, ac []task.AcceptanceCriterion, taskID string) (res Result, err error) {
	if !r.validateTaskID(taskID) {
		return Result{}, fmt.Errorf("invalid task id: %s", taskID)
	}

	startedAt := time.Now().UTC()
	runID, err := newRunID()
	if err != nil {
		return Result{}, err
	}
	res.RunID = runID

	defer func() {
		status := res.Status
		if status == "" && err != nil {
			status = statusError
		}
		event := log.Info().
			Str("run_id", runID).
			Str("status", status).
			Str("duration", time.Since(startedAt).String())

		if err != nil {
			event = event.Err(err)
		}
		event.Msg("run finished")
	}()

	lock, err := AcquireRunLock(r.normaDir)
	if err != nil {
		return res, fmt.Errorf("acquire run lock: %w", err)
	}
	defer func() {
		if lErr := lock.Release(); lErr != nil {
			log.Warn().Err(lErr).Msg("failed to release run lock")
		}
	}()

	if err := os.MkdirAll(r.normaDir, 0o755); err != nil {
		return res, fmt.Errorf("create .norma: %w", err)
	}

	baseBranch, err := git.CurrentBranch(ctx, r.repoRoot)
	if err != nil {
		return res, fmt.Errorf("resolve base branch: %w", err)
	}
	log.Info().Str("base_branch", baseBranch).Msg("using local base branch for task sync")

	// Prune stalled worktrees
	_ = git.RunCmdErr(ctx, r.repoRoot, "git", "worktree", "prune")

	if err := reconcile.Run(ctx, r.store.DB(), r.normaDir); err != nil {
		return res, err
	}

	runDir := filepath.Join(r.normaDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return res, fmt.Errorf("create run dir: %w", err)
	}

	if err := r.store.CreateRun(ctx, runID, goal, runDir, 1); err != nil {
		return res, fmt.Errorf("create run in store: %w", err)
	}

	input := workflows.RunInput{
		RunID:              runID,
		Goal:               goal,
		AcceptanceCriteria: ac,
		TaskID:             taskID,
		RunDir:             runDir,
		GitRoot:            r.repoRoot,
		BaseBranch:         baseBranch,
	}

	wfRes, err := r.workflow.Run(ctx, input)
	if err != nil {
		return res, fmt.Errorf("execute workflow: %w", err)
	}

	res.Status = wfRes.Status

	if wfRes.Verdict != nil && *wfRes.Verdict == "PASS" {
		log.Info().Msg("verdict is PASS, applying changes")
		err = r.applyChanges(ctx, runID, goal, taskID)
		if err != nil {
			log.Error().Err(err).Msg("failed to apply changes")
			return res, fmt.Errorf("apply changes: %w", err)
		}
		// Close task in Beads as per spec
		if err := r.tracker.MarkStatus(ctx, taskID, "done"); err != nil {
			log.Warn().Err(err).Msg("failed to mark task as done in beads")
		}
		res.Status = statusPassed
	}

	return res, nil
}

func (r *Runner) applyChanges(ctx context.Context, runID, goal, taskID string) error {
	branchName := fmt.Sprintf("norma/task/%s", taskID)
	commitMsg := fmt.Sprintf("feat: %s\n\nRun: %s\nTask: %s", goal, runID, taskID)

	log.Info().Str("branch", branchName).Msg("applying changes from workspace")

	// Ensure a clean working tree before merge to avoid clobbering local changes.
	dirty := strings.TrimSpace(git.RunCmd(ctx, r.repoRoot, "git", "status", "--porcelain"))
	stashed := false
	if dirty != "" {
		log.Info().Msg("stashing local changes before merge")
		if err := git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "push", "-u", "-m", fmt.Sprintf("norma pre-apply %s", runID)); err != nil {
			return fmt.Errorf("git stash push: %w", err)
		}
		stashed = true
	}

	// record git status/hash "before"
	beforeHash := strings.TrimSpace(git.RunCmd(ctx, r.repoRoot, "git", "rev-parse", "HEAD"))

	// merge --squash
	if err := git.RunCmdErr(ctx, r.repoRoot, "git", "merge", "--squash", branchName); err != nil {
		if stashed {
			_ = git.RunCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
			_ = git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git merge --squash: %w", err)
	}

	if stashed {
		if err := git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "apply"); err != nil {
			_ = git.RunCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
			return fmt.Errorf("git stash apply: %w", err)
		}
	}

	if err := git.RunCmdErr(ctx, r.repoRoot, "git", "add", "-A"); err != nil {
		_ = git.RunCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
		if stashed {
			_ = git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git add -A: %w", err)
	}

	// check if there are changes to commit
	status := git.RunCmd(ctx, r.repoRoot, "git", "status", "--porcelain")
	log.Debug().Str("git_status", status).Msg("git status after merge")
	if strings.TrimSpace(status) == "" {
		log.Info().Msg("nothing to commit after merge")
		return nil
	}

	// commit using Conventional Commits
	if err := git.RunCmdErr(ctx, r.repoRoot, "git", "commit", "-m", commitMsg); err != nil {
		log.Error().Err(err).Msg("failed to commit merged changes, rolling back")
		_ = git.RunCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
		if stashed {
			_ = git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git commit: %w", err)
	}

	if stashed {
		if err := git.RunCmdErr(ctx, r.repoRoot, "git", "stash", "drop"); err != nil {
			log.Warn().Err(err).Msg("failed to drop applied stash")
		}
	}

	afterHash := strings.TrimSpace(git.RunCmd(ctx, r.repoRoot, "git", "rev-parse", "HEAD"))
	log.Info().
		Str("before_hash", beforeHash).
		Str("after_hash", afterHash).
		Msg("changes applied and committed successfully")

	return nil
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
