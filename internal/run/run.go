package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/reconcile"
	"github.com/metalagman/norma/internal/task"
	"github.com/rs/zerolog/log"
)

// Runner executes the norma workflow for a run.
type Runner struct {
	repoRoot     string
	normaDir     string
	cfg          config.Config
	store        *Store
	agents       map[string]agent.Runner
	tracker      task.Tracker
	taskID       string
	workspaceDir string
	artifactsDir string
	state        model.TaskState
}

// Result summarizes a completed run.
type Result struct {
	RunID   string
	Status  string
	Verdict *string
}

// NewRunner constructs a Runner with agent implementations.
func NewRunner(repoRoot string, cfg config.Config, store *Store, tracker task.Tracker) (*Runner, error) {
	agents := make(map[string]agent.Runner)
	for _, role := range []string{"plan", "do", "check", "act"} {
		agentCfg, ok := cfg.Agents[role]
		if !ok {
			return nil, fmt.Errorf("missing agent config for role %q", role)
		}
		runner, err := agent.NewRunner(agentCfg, repoRoot)
		if err != nil {
			return nil, fmt.Errorf("init %s agent: %w", role, err)
		}
		agents[role] = runner
	}
	return &Runner{
		repoRoot: repoRoot,
		normaDir: filepath.Join(repoRoot, ".norma"),
		cfg:      cfg,
		store:    store,
		agents:   agents,
		tracker:  tracker,
	}, nil
}

func (r *Runner) validateTaskID(id string) bool {
	matched, _ := regexp.MatchString(`^norma-[a-z0-9]+$`, id)
	return matched
}

// Run starts a new run with the given goal and acceptance criteria.
func (r *Runner) Run(ctx context.Context, goal string, ac []model.AcceptanceCriterion, taskID string) (res Result, err error) {
	if !r.validateTaskID(taskID) {
		return Result{}, fmt.Errorf("invalid task id: %s", taskID)
	}

	r.taskID = taskID
	startedAt := time.Now().UTC()
	defer func() {
		if res.RunID == "" {
			return
		}
		if r.workspaceDir != "" {
			_ = cleanupWorkspace(ctx, r.repoRoot, r.workspaceDir, r.taskID)
		}
		status := res.Status
		if status == "" && err != nil {
			status = "error"
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
	defer lock.Release()

	if err := os.MkdirAll(r.normaDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create .norma: %w", err)
	}
	if err := reconcile.Run(ctx, r.store.db, r.normaDir); err != nil {
		return Result{}, err
	}

	runID, err := newRunID()
	if err != nil {
		return Result{}, err
	}

	runDir := filepath.Join(r.normaDir, "runs", runID)
	r.artifactsDir = filepath.Join(runDir, "artifacts")
	if err := os.MkdirAll(r.artifactsDir, 0o755); err != nil {
		return Result{RunID: runID}, fmt.Errorf("create artifacts dir: %w", err)
	}

	r.workspaceDir, err = createWorkspace(ctx, r.repoRoot, runDir, r.taskID)
	if err != nil {
		return Result{RunID: runID}, fmt.Errorf("create workspace: %w", err)
	}

	stepsDir := filepath.Join(runDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return Result{RunID: runID}, fmt.Errorf("create run steps: %w", err)
	}

	if err := r.store.CreateRun(ctx, runID, goal, runDir, 1); err != nil {
		return Result{RunID: runID}, err
	}

	taskItem, err := r.tracker.Get(ctx, r.taskID)
	if err != nil {
		return Result{RunID: runID}, fmt.Errorf("get task: %w", err)
	}

	iteration := 1
	stepIndex := 0

	var lastPlan *model.PlanOutput
	var lastDo *model.DoOutput
	var lastCheck *model.CheckOutput

	// Load existing state
	if taskItem.Notes != "" {
		if err := json.Unmarshal([]byte(taskItem.Notes), &r.state); err == nil {
			log.Info().Str("task_id", r.taskID).Msg("loaded existing state from task notes")
			lastPlan = r.state.Plan
			lastDo = r.state.Do
			lastCheck = r.state.Check

			// Reconstruct progress.md from journal
			if len(r.state.Journal) > 0 {
				path := filepath.Join(r.artifactsDir, "progress.md")
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
		// 1. PLAN
		if iteration == 1 && hasLabel("norma-has-plan") && lastPlan != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping plan: norma-has-plan label present")
		} else {
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-plan")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-do")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-check")

			stepIndex++
			r.tracker.MarkStatus(ctx, r.taskID, "planning")
			planReq := r.baseRequest(runID, iteration, stepIndex, "plan", goal, ac)
			planReq.Plan = &model.PlanInput{Task: model.IDInfo{ID: r.taskID}}

			planRes, err := r.runAndCommitStep(ctx, planReq, stepsDir)
			if err != nil {
				return Result{RunID: runID}, err
			}
			if planRes.Status != "ok" && (planRes.Response == nil || planRes.Response.Plan == nil) {
				return r.handleStop(ctx, runID, iteration, stepIndex, planRes)
			}
			lastPlan = planRes.Response.Plan

			// Persist plan
			r.state.Plan = lastPlan
			r.persistState(ctx)
			_ = r.tracker.AddLabel(ctx, r.taskID, "norma-has-plan")
		}

		// 2. DO
		if iteration == 1 && hasLabel("norma-has-do") && lastDo != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping do: norma-has-do label present")
		} else {
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-do")
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-check")

			stepIndex++
			r.tracker.MarkStatus(ctx, r.taskID, "doing")
			doReq := r.baseRequest(runID, iteration, stepIndex, "do", goal, ac)
			doReq.Do = &model.DoInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
			}

			doRes, err := r.runAndCommitStep(ctx, doReq, stepsDir)
			if err != nil {
				return Result{RunID: runID}, err
			}
			if doRes.Status != "ok" && (doRes.Response == nil || doRes.Response.Do == nil) {
				return r.handleStop(ctx, runID, iteration, stepIndex, doRes)
			}
			lastDo = doRes.Response.Do

			// Persist do
			r.state.Do = lastDo
			r.persistState(ctx)
			_ = r.tracker.AddLabel(ctx, r.taskID, "norma-has-do")
		}

		// 3. CHECK
		if iteration == 1 && hasLabel("norma-has-check") && lastCheck != nil {
			log.Info().Str("task_id", r.taskID).Msg("skipping check: norma-has-check label present")
		} else {
			_ = r.tracker.RemoveLabel(ctx, r.taskID, "norma-has-check")

			stepIndex++
			r.tracker.MarkStatus(ctx, r.taskID, "checking")
			checkReq := r.baseRequest(runID, iteration, stepIndex, "check", goal, ac)
			checkReq.Check = &model.CheckInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
				DoExecution:       lastDo.Execution,
			}

			checkRes, err := r.runAndCommitStep(ctx, checkReq, stepsDir)
			if err != nil {
				return Result{RunID: runID}, err
			}
			if checkRes.Status != "ok" && (checkRes.Response == nil || checkRes.Response.Check == nil) {
				return r.handleStop(ctx, runID, iteration, stepIndex, checkRes)
			}
			lastCheck = checkRes.Response.Check

			// Persist check
			r.state.Check = lastCheck
			r.persistState(ctx)
			_ = r.tracker.AddLabel(ctx, r.taskID, "norma-has-check")
		}

		// 4. ACT
		stepIndex++
		r.tracker.MarkStatus(ctx, r.taskID, "acting")
		actReq := r.baseRequest(runID, iteration, stepIndex, "act", goal, ac)
		actReq.Act = &model.ActInput{
			CheckVerdict:      lastCheck.Verdict,
			AcceptanceResults: lastCheck.AcceptanceResults,
		}

		actRes, err := r.runAndCommitStep(ctx, actReq, stepsDir)
		if err != nil {
			return Result{RunID: runID}, err
		}

		if lastCheck.Verdict.Status == "PASS" {
			err = r.applyChanges(ctx, runID, goal)
			if err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "passed"}, nil
		}

		if actRes.Status == "stop" || actRes.Status == "error" || (actRes.Response != nil && actRes.Response.Act != nil && actRes.Response.Act.Decision == "close") {
			return r.handleStop(ctx, runID, iteration, stepIndex, actRes)
		}

		iteration++
	}

	return Result{RunID: runID, Status: "stopped"}, nil
}

func (r *Runner) baseRequest(runID string, iteration, index int, role, goal string, ac []model.AcceptanceCriterion) model.AgentRequest {
	return model.AgentRequest{
		Run: model.RunInfo{
			ID:        runID,
			Iteration: iteration,
		},
		Task: model.TaskInfo{
			ID:                 r.taskID,
			Title:              goal,
			AcceptanceCriteria: ac,
		},
		Step: model.StepInfo{
			Index: index,
			Name:  role,
		},
		Paths: model.RequestPaths{
			WorkspaceDir: r.workspaceDir,
			WorkspaceMode: "read_only",
		},
		Budgets: model.Budgets{
			MaxIterations: r.cfg.Budgets.MaxIterations,
		},
		StopReasonsAllowed: []string{
			"budget_exceeded",
			"dependency_blocked",
			"verify_missing",
			"replan_required",
		},
		Context: model.RequestContext{
			Facts: make(map[string]any),
		},
	}
}

func (r *Runner) runAndCommitStep(ctx context.Context, req model.AgentRequest, stepsDir string) (stepResult, error) {
	res, err := executeStep(ctx, r.agents[req.Step.Name], req, stepsDir)
	if err != nil {
		return res, err
	}

	err = r.commitStep(ctx, req.Run.ID, res, "running", nil)
	if err != nil {
		return res, err
	}

	r.appendToProgress(res)

	return res, nil
}

func (r *Runner) handleStop(ctx context.Context, runID string, iteration, index int, res stepResult) (Result, error) {
	status := "failed"
	if res.Status == "stop" {
		status = "stopped"
	}
	r.store.UpdateRun(ctx, runID, RunUpdate{
		Status:           status,
		Iteration:        iteration,
		CurrentStepIndex: index,
	}, nil)
	return Result{RunID: runID, Status: status}, nil
}

func (r *Runner) persistState(ctx context.Context) {
	data, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal task state")
		return
	}
	_ = r.tracker.SetNotes(ctx, r.taskID, string(data))
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

	entry := model.JournalEntry{
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

	// Update artifacts/progress.md
	path := filepath.Join(r.artifactsDir, "progress.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Error().Err(err).Msg("failed to open progress.md")
		return
	}
	defer f.Close()

	md := fmt.Sprintf("## %s — %d %s — %s/%s\n", entry.Timestamp, entry.StepIndex, strings.ToUpper(entry.Role), entry.Status, entry.StopReason)
	md += fmt.Sprintf("**Task:** %s  \n", r.taskID)
	md += fmt.Sprintf("**Run:** %s · **Iteration:** %d\n\n", res.FinalDir, res.Iteration)
	md += fmt.Sprintf("**Title:** %s\n\n", entry.Title)
	if len(entry.Details) > 0 {
		md += "**Details:**\n"
		for _, detail := range entry.Details {
			md += fmt.Sprintf("- %s\n", detail)
		}
	}
	md += "\n**Logs:**\n"
	md += fmt.Sprintf("- stdout: %s\n", entry.Logs.StdoutPath)
	md += fmt.Sprintf("- stderr: %s\n\n", entry.Logs.StderrPath)

	_, _ = f.WriteString(md)
}

func (r *Runner) applyChanges(ctx context.Context, runID, goal string) error {
	branchName := fmt.Sprintf("norma/task/%s", r.taskID)
	commitMsg := fmt.Sprintf("feat: %s\n\nRun: %s\nTask: %s", goal, runID, r.taskID)

	log.Info().Str("branch", branchName).Msg("applying changes from workspace")

	// record git status/hash "before"
	beforeHash := strings.TrimSpace(runCmd(ctx, r.repoRoot, "git", "rev-parse", "HEAD"))

	// merge --squash
	if err := runCmdErr(ctx, r.repoRoot, "git", "merge", "--squash", branchName); err != nil {
		return fmt.Errorf("git merge --squash: %w", err)
	}

	// check if there are changes to commit
	status := runCmd(ctx, r.repoRoot, "git", "status", "--porcelain")
	if strings.TrimSpace(status) == "" {
		log.Info().Msg("nothing to commit after merge")
		return nil
	}

	// commit using Conventional Commits
	if err := runCmdErr(ctx, r.repoRoot, "git", "commit", "-m", commitMsg); err != nil {
		log.Error().Err(err).Msg("failed to commit merged changes, rolling back")
		_ = runCmdErr(ctx, r.repoRoot, "git", "reset", "--hard", beforeHash)
		return fmt.Errorf("git commit: %w", err)
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
	update := RunUpdate{
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