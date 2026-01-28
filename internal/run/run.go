// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/reconcile"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/rs/zerolog/log"
)

const (
	statusError   = "error"
	statusFailed  = "failed"
	statusPassed  = "passed"
	statusRunning = "running"
	statusStopped = "stopped"
	statusStop    = "stop"
	statusOK      = "ok"

	labelHasPlan  = "norma-has-plan"
	labelHasDo    = "norma-has-do"
	labelHasCheck = "norma-has-check"
)

// Runner executes the norma workflow for a run.
type Runner struct {
	repoRoot string
	normaDir string
	runDir   string
	cfg      config.Config
	store    *Store
	tracker  task.Tracker
	taskID   string
	state    normaloop.TaskState
}

// Result summarizes a completed run.
type Result struct {
	RunID   string
	Status  string
	Verdict *string
}

// NewRunner constructs a Runner with agent implementations.
func NewRunner(repoRoot string, cfg config.Config, store *Store, tracker task.Tracker) (*Runner, error) {
	for _, roleName := range []string{normaloop.RolePlan, normaloop.RoleDo, normaloop.RoleCheck, normaloop.RoleAct} {
		role := normaloop.GetRole(roleName)
		if role == nil {
			return nil, fmt.Errorf("unknown role %q", roleName)
		}
		agentCfg, ok := cfg.Agents[roleName]
		if !ok {
			return nil, fmt.Errorf("missing agent config for role %q", roleName)
		}
		roleRunner, err := agent.NewRunner(agentCfg, role)
		if err != nil {
			return nil, fmt.Errorf("init %s agent: %w", roleName, err)
		}
		role.SetRunner(roleRunner)
	}
	return &Runner{
		repoRoot: repoRoot,
		normaDir: filepath.Join(repoRoot, ".norma"),
		cfg:      cfg,
		store:    store,
		tracker:  tracker,
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

	r.taskID = taskID
	startedAt := time.Now().UTC()
	defer func() {
		if res.RunID == "" {
			return
		}
		status := res.Status
		if status == "" && err != nil {
			status = statusError
		}
		event := log.Info().
			Str("run_id", res.RunID).
			Str("status", status).
			Dur("duration", time.Since(startedAt))
		if err != nil {
			event = event.Err(err)
		}
		event.Msg("run finished")
	}()

	lock, err := AcquireRunLock(r.normaDir)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if lErr := lock.Release(); lErr != nil {
			log.Warn().Err(lErr).Msg("failed to release run lock")
		}
	}()

	if err := os.MkdirAll(r.normaDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create .norma: %w", err)
	}

	// Prune stalled worktrees
	_ = runCmdErr(ctx, r.repoRoot, "git", "worktree", "prune")

	if err := reconcile.Run(ctx, r.store.db, r.normaDir); err != nil {
		return Result{}, err
	}

	runID, err := newRunID()
	if err != nil {
		return Result{}, err
	}

	r.runDir = filepath.Join(r.normaDir, "runs", runID)

	stepsDir := filepath.Join(r.runDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return Result{RunID: runID}, fmt.Errorf("create run steps: %w", err)
	}

	if err := r.store.CreateRun(ctx, runID, goal, r.runDir, 1); err != nil {
		return Result{RunID: runID}, err
	}

	taskItem, err := r.tracker.Task(ctx, r.taskID)
	if err != nil {
		return Result{RunID: runID}, fmt.Errorf("get task: %w", err)
	}

	iteration := 1
	stepIndex := 0

	var lastPlan *normaloop.PlanOutput
	var lastDo *normaloop.DoOutput
	var lastCheck *normaloop.CheckOutput
	var lastAct *normaloop.ActOutput

	// Load existing state
	if taskItem.Notes != "" {
		if err := json.Unmarshal([]byte(taskItem.Notes), &r.state); err == nil {
			log.Info().Str("task_id", r.taskID).Msg("loaded existing state from task notes")
			lastPlan = r.state.Plan
			lastDo = r.state.Do
			lastCheck = r.state.Check
		}
	}

	hasLabel := func(name string) bool {
		for _, l := range taskItem.Labels {
			if l == name {
				return true
			}
		}
		return false
	}

	for iteration <= r.cfg.Budgets.MaxIterations {
		log.Info().Int("iteration", iteration).Msg("starting iteration")
		// 1. PLAN
		skipPlan := false
		if iteration == 1 && hasLabel(labelHasPlan) && lastPlan != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping plan: norma-has-plan label present")
			skipPlan = true
		} else if iteration > 1 && lastAct != nil && lastAct.Decision == "continue" && lastPlan != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping plan: Act decision was 'continue'")
			skipPlan = true
		}

		if !skipPlan {
			log.Info().Msg("executing plan step")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasPlan)
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasDo)
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasCheck)

			stepIndex++
			if err := r.tracker.MarkStatus(ctx, r.taskID, "planning"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to planning")
			}
			planReq := r.baseRequest(runID, iteration, stepIndex, normaloop.RolePlan, goal, ac)
			planReq.Plan = &normaloop.PlanInput{Task: normaloop.IDInfo{ID: r.taskID}}

			planRes, err := r.runAndCommitStep(ctx, planReq, stepsDir)
			if err != nil {
				log.Error().Err(err).Msg("plan step execution failed with error")
				return Result{RunID: runID}, err
			}
			if planRes.Status != statusOK && (planRes.Response == nil || planRes.Response.Plan == nil) {
				log.Warn().Str("status", planRes.Status).Msg("plan step failed without required data, stopping")
				return r.handleStop(ctx, runID, iteration, stepIndex, planRes)
			}
			lastPlan = planRes.Response.Plan

			// Persist plan
			r.state.Plan = lastPlan
			if err := r.persistState(ctx); err != nil {
				log.Warn().Err(err).Msg("failed to persist state after plan")
			}
			_ = r.tracker.AddLabel(ctx, r.taskID, labelHasPlan)
		}

		// 2. DO
		if iteration == 1 && hasLabel(labelHasDo) && lastDo != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping do: norma-has-do label present")
		} else {
			log.Info().Msg("executing do step")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasDo)
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasCheck)

			stepIndex++
			if err := r.tracker.MarkStatus(ctx, r.taskID, "doing"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to doing")
			}
			doReq := r.baseRequest(runID, iteration, stepIndex, normaloop.RoleDo, goal, ac)
			doReq.Do = &normaloop.DoInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
			}

			doRes, err := r.runAndCommitStep(ctx, doReq, stepsDir)
			if err != nil {
				log.Error().Err(err).Msg("do step execution failed with error")
				return Result{RunID: runID}, err
			}
			if doRes.Status != statusOK && (doRes.Response == nil || doRes.Response.Do == nil) {
				log.Warn().Str("status", doRes.Status).Msg("do step failed without required data, stopping")
				return r.handleStop(ctx, runID, iteration, stepIndex, doRes)
			}

			lastDo = doRes.Response.Do

			// Persist do
			r.state.Do = lastDo
			if err := r.persistState(ctx); err != nil {
				log.Warn().Err(err).Msg("failed to persist state after do")
			}
			_ = r.tracker.AddLabel(ctx, r.taskID, labelHasDo)
		}

		// 3. CHECK
		if iteration == 1 && hasLabel(labelHasCheck) && lastCheck != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping check: norma-has-check label present")
		} else {
			log.Info().Msg("executing check step")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasCheck)

			stepIndex++
			if err := r.tracker.MarkStatus(ctx, r.taskID, "checking"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to checking")
			}
			checkReq := r.baseRequest(runID, iteration, stepIndex, normaloop.RoleCheck, goal, ac)
			checkReq.Check = &normaloop.CheckInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
				DoExecution:       lastDo.Execution,
			}

			checkRes, err := r.runAndCommitStep(ctx, checkReq, stepsDir)
			if err != nil {
				log.Error().Err(err).Msg("check step execution failed with error")
				return Result{RunID: runID}, err
			}
			if checkRes.Status != statusOK && (checkRes.Response == nil || checkRes.Response.Check == nil) {
				log.Warn().Str("status", checkRes.Status).Msg("check step failed without required data, stopping")
				return r.handleStop(ctx, runID, iteration, stepIndex, checkRes)
			}

			if checkRes.Response == nil || checkRes.Response.Check == nil {
				log.Error().Str("status", checkRes.Status).Msg("check step finished but Response.Check is nil")
				return r.failRun(ctx, runID, iteration, stepIndex, "check step produced no verdict data")
			}

			lastCheck = checkRes.Response.Check

			// Persist check
			r.state.Check = lastCheck
			if err := r.persistState(ctx); err != nil {
				log.Warn().Err(err).Msg("failed to persist state after check")
			}
			_ = r.tracker.AddLabel(ctx, r.taskID, labelHasCheck)
		}

		// 4. ACT
		log.Info().Msg("preparing act step")
		if lastCheck == nil {
			log.Error().Msg("lastCheck is nil before ACT, this should not happen")
			return r.failRun(ctx, runID, iteration, stepIndex, "internal error: missing check verdict for act")
		}

		stepIndex++
		if err := r.tracker.MarkStatus(ctx, r.taskID, "acting"); err != nil {
			log.Warn().Err(err).Msg("failed to update task status to acting")
		}
		actReq := r.baseRequest(runID, iteration, stepIndex, normaloop.RoleAct, goal, ac)
		actReq.Act = &normaloop.ActInput{
			CheckVerdict:      lastCheck.Verdict,
			AcceptanceResults: lastCheck.AcceptanceResults,
		}

		actRes, err := r.runAndCommitStep(ctx, actReq, stepsDir)
		if err != nil {
			log.Error().Err(err).Msg("act step execution failed with error")
			return Result{RunID: runID}, err
		}

		if actRes.Response != nil && actRes.Response.Act != nil {
			lastAct = actRes.Response.Act
			r.state.Act = lastAct
			if lastAct.Decision == "replan" {
				log.Info().Msg("act decision is replan, clearing has-plan label")
				_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasPlan)
				_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasDo)
				_ = r.tracker.RemoveLabel(ctx, r.taskID, labelHasCheck)
				lastPlan = nil
				r.state.Plan = nil
				if err := r.persistState(ctx); err != nil {
					log.Warn().Err(err).Msg("failed to persist state after act")
				}
			}
		}

		log.Info().Str("verdict", lastCheck.Verdict.Status).Msg("evaluating verdict")
		if lastCheck.Verdict.Status == "PASS" {
			log.Info().Msg("verdict is PASS, applying changes")
			err = r.applyChanges(ctx, runID, goal)
			if err != nil {
				log.Error().Err(err).Msg("failed to apply changes")
				return Result{RunID: runID}, err
			}
			// Close task in Beads as per spec
			if err := r.tracker.MarkStatus(ctx, r.taskID, "done"); err != nil {
				log.Warn().Err(err).Msg("failed to mark task as done in beads")
			}
			return Result{RunID: runID, Status: statusPassed}, nil
		}

		log.Info().Str("act_status", actRes.Status).Msg("evaluating act decision")
		if actRes.Status == statusStop || actRes.Status == statusError || (actRes.Response != nil && actRes.Response.Act != nil && actRes.Response.Act.Decision == "close") {
			log.Info().Msg("act decision is stop or close, stopping run")
			return r.handleStop(ctx, runID, iteration, stepIndex, actRes)
		}

		log.Info().Msg("continuing to next iteration")
		iteration++
	}

	return Result{RunID: runID, Status: statusStopped}, nil
}

func (r *Runner) baseRequest(runID string, iteration, index int, role, goal string, ac []task.AcceptanceCriterion) normaloop.AgentRequest {
	return normaloop.AgentRequest{
		Run: normaloop.RunInfo{
			ID:        runID,
			Iteration: iteration,
		},
		Task: normaloop.TaskInfo{
			ID:                 r.taskID,
			Title:              goal,
			AcceptanceCriteria: ac,
		},
		Step: normaloop.StepInfo{
			Index: index,
			Name:  role,
		},
		Paths: normaloop.RequestPaths{
		},
		Budgets: normaloop.Budgets{
			MaxIterations: r.cfg.Budgets.MaxIterations,
		},
		StopReasonsAllowed: []string{
			"budget_exceeded",
			"dependency_blocked",
			"verify_missing",
			"replan_required",
		},
		Context: normaloop.RequestContext{
			Facts: make(map[string]any),
		},
	}
}

func (r *Runner) runAndCommitStep(ctx context.Context, req normaloop.AgentRequest, stepsDir string) (stepResult, error) {
	maxAttempts := 3
	var lastRes stepResult
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req.Context.Attempt = attempt
		res, err := r.executeStep(ctx, req, stepsDir)
		lastRes = res
		lastErr = err

		if err == nil {
			err = r.commitStep(ctx, req.Run.ID, res, statusRunning, nil)
			if err != nil {
				return res, err
			}
			r.appendToProgress(res)
			return res, nil
		}

		if !errors.Is(err, ErrRetryable) {
			break
		}

		log.Warn().
			Str("role", req.Step.Name).
			Int("attempt", attempt).
			Err(err).
			Msg("step failed with retryable error, retrying...")

		// Optional: add a small delay between retries
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(time.Second * time.Duration(attempt+1)):
		}
	}

	// If we reach here, it means all attempts failed or a non-retryable error occurred
	// We still commit the last failed attempt if it was retryable
	if errors.Is(lastErr, ErrRetryable) {
		_ = r.commitStep(ctx, req.Run.ID, lastRes, statusRunning, nil)
		r.appendToProgress(lastRes)
	}

	return lastRes, lastErr
}

func (r *Runner) handleStop(ctx context.Context, runID string, iteration, index int, res stepResult) (Result, error) {
	status := statusFailed
	if res.Status == statusStop {
		status = statusStopped
	}
	if err := r.store.UpdateRun(ctx, runID, Update{
		Status:           status,
		Iteration:        iteration,
		CurrentStepIndex: index,
	}, nil); err != nil {
		log.Warn().Err(err).Msg("failed to update run status")
	}

	// Update task status in Beads
	if err := r.tracker.MarkStatus(ctx, r.taskID, status); err != nil {
		log.Warn().Err(err).Msg("failed to update task status in beads")
	}

	return Result{RunID: runID, Status: status}, nil
}

func (r *Runner) failRun(ctx context.Context, runID string, iteration, stepIndex int, reason string) (Result, error) {
	update := Update{
		CurrentStepIndex: stepIndex,
		Iteration:        iteration,
		Status:           statusFailed,
		Verdict:          nil,
	}
	event := Event{Type: "run_failed", Message: reason, DataJSON: ""}
	if err := r.store.UpdateRun(ctx, runID, update, &event); err != nil {
		return Result{RunID: runID}, err
	}

	// Update task status in Beads
	if err := r.tracker.MarkStatus(ctx, r.taskID, statusFailed); err != nil {
		log.Warn().Err(err).Msg("failed to update task status in beads")
	}

	return Result{RunID: runID, Status: statusFailed}, nil
}

func (r *Runner) persistState(ctx context.Context) error {
	data, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task state: %w", err)
	}
	return r.tracker.SetNotes(ctx, r.taskID, string(data))
}

func (r *Runner) appendToProgress(res stepResult) {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	stopReason := res.Protocol
	if res.Response != nil && res.Response.StopReason != "" {
		stopReason = res.Response.StopReason
	}
	if stopReason == "" {
		stopReason = "none"
	}

	entry := normaloop.JournalEntry{
		Timestamp:  timestamp,
		StepIndex:  res.StepIndex,
		Role:       res.Role,
		Status:     res.Status,
		StopReason: stopReason,
	}

	if res.Response != nil {
		entry.Title = res.Response.Progress.Title
		entry.Details = res.Response.Progress.Details
		entry.Logs = res.Response.Logs
	}

	if entry.Title == "" {
		entry.Title = fmt.Sprintf("%s step completed", res.Role)
	}

	r.state.Journal = append(r.state.Journal, entry)

	// Reconstruct progress.md in run directory
	path := filepath.Join(r.runDir, "progress.md")
	var b strings.Builder
	for _, entry := range r.state.Journal {
		b.WriteString(fmt.Sprintf("## %s — %d %s — %s/%s\n", entry.Timestamp, entry.StepIndex, strings.ToUpper(entry.Role), entry.Status, entry.StopReason))
		b.WriteString(fmt.Sprintf("**Task:** %s  \n", r.taskID))
		b.WriteString(fmt.Sprintf("**Title:** %s\n\n", entry.Title))
		if len(entry.Details) > 0 {
			b.WriteString("**Details:**\n")
			for _, detail := range entry.Details {
				b.WriteString(fmt.Sprintf("- %s\n", detail))
			}
		}
		b.WriteString("\n**Logs:**\n")
		b.WriteString(fmt.Sprintf("- stdout: %s\n", entry.Logs.StdoutPath))
		b.WriteString(fmt.Sprintf("- stderr: %s\n\n", entry.Logs.StderrPath))
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

func (r *Runner) applyChanges(ctx context.Context, runID, goal string) error {
	branchName := fmt.Sprintf("norma/task/%s", r.taskID)
	commitMsg := fmt.Sprintf("feat: %s\n\nRun: %s\nTask: %s", goal, runID, r.taskID)

	log.Info().Str("branch", branchName).Msg("applying changes from workspace")

	// Ensure a clean working tree before merge to avoid clobbering local changes.
	dirty := strings.TrimSpace(runCmd(ctx, r.repoRoot, "git", "status", "--porcelain"))
	stashed := false
	if dirty != "" {
		log.Info().Msg("stashing local changes before merge")
		if err := runCmdErr(ctx, r.repoRoot, "git", "stash", "push", "-u", "-m", fmt.Sprintf("norma pre-apply %s", runID)); err != nil {
			return fmt.Errorf("git stash push: %w", err)
		}
		stashed = true
	}

	// record git status/hash "before"
	beforeHash := strings.TrimSpace(runCmd(ctx, r.repoRoot, "git", "rev-parse", "HEAD"))

	// merge --squash
	if err := runCmdErr(ctx, r.repoRoot, "git", "merge", "--squash", branchName); err != nil {
		if stashed {
			_ = runCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
			_ = runCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git merge --squash: %w", err)
	}

	if stashed {
		if err := runCmdErr(ctx, r.repoRoot, "git", "stash", "apply"); err != nil {
			_ = runCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
			return fmt.Errorf("git stash apply: %w", err)
		}
	}

	if err := runCmdErr(ctx, r.repoRoot, "git", "add", "-A"); err != nil {
		_ = runCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
		if stashed {
			_ = runCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git add -A: %w", err)
	}

	// check if there are changes to commit
	status := runCmd(ctx, r.repoRoot, "git", "status", "--porcelain")
	log.Debug().Str("git_status", status).Msg("git status after merge")
	if strings.TrimSpace(status) == "" {
		log.Info().Msg("nothing to commit after merge")
		return nil
	}

	// commit using Conventional Commits
	if err := runCmdErr(ctx, r.repoRoot, "git", "commit", "-m", commitMsg); err != nil {
		log.Error().Err(err).Msg("failed to commit merged changes, rolling back")
		_ = runCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
		if stashed {
			_ = runCmdErr(ctx, r.repoRoot, "git", "stash", "pop")
		}
		return fmt.Errorf("git commit: %w", err)
	}

	if stashed {
		if err := runCmdErr(ctx, r.repoRoot, "git", "stash", "drop"); err != nil {
			log.Warn().Err(err).Msg("failed to drop applied stash")
		}
	}

	afterHash := strings.TrimSpace(runCmd(ctx, r.repoRoot, "git", "rev-parse", "HEAD"))
	log.Info().
		Str("before_hash", beforeHash).
		Str("after_hash", afterHash).
		Msg("changes applied and committed successfully")

	return nil
}

func (r *Runner) commitStep(ctx context.Context, runID string, res stepResult, runStatus string, verdict *string) error {
	step := StepRecord{
		RunID:     runID,
		StepIndex: res.StepIndex,
		Role:      res.Role,
		Iteration: res.Iteration,
		Status:    res.Status,
		StepDir:   res.FinalDir,
		StartedAt: res.StartedAt.UTC().Format(time.RFC3339),
		EndedAt:   res.EndedAt.UTC().Format(time.RFC3339),
		Summary:   res.Summary,
	}
	update := Update{
		CurrentStepIndex: res.StepIndex,
		Iteration:        res.Iteration,
		Status:           runStatus,
		Verdict:          verdict,
	}
	return r.store.CommitStep(ctx, step, nil, update)
}

func newRunID() (string, error) {
	suffix, err := randomHex(3)
	if err != nil {
		return "", err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", ts, suffix), nil
}
