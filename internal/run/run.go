package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/reconcile"
	"github.com/rs/zerolog/log"
)

// Runner executes the norma workflow for a run.
type Runner struct {
	repoRoot string
	normaDir string
	cfg      config.Config
	store    *Store
	agents   map[string]agent.Runner
}

// Result summarizes a completed run.
type Result struct {
	RunID   string
	Status  string
	Verdict *string
}

// NewRunner constructs a Runner with agent implementations.
func NewRunner(repoRoot string, cfg config.Config, store *Store) (*Runner, error) {
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
	}, nil
}

// Run starts a new run with the given goal and acceptance criteria.
func (r *Runner) Run(ctx context.Context, goal string, ac []model.AcceptanceCriterion) (res Result, err error) {
	startedAt := time.Now().UTC()
	defer func() {
		if res.RunID == "" {
			return
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
	if r.cfg.Retention.KeepLast > 0 || r.cfg.Retention.KeepDays > 0 {
		policy := RetentionPolicy{KeepLast: r.cfg.Retention.KeepLast, KeepDays: r.cfg.Retention.KeepDays}
		if res, err := PruneRuns(ctx, r.store.db, filepath.Join(r.normaDir, "runs"), policy, false); err != nil {
			return Result{}, err
		} else {
			log.Info().
				Str("operation", "auto-prune").
				Int("keep_last", policy.KeepLast).
				Int("keep_days", policy.KeepDays).
				Int("considered", res.Considered).
				Int("kept", res.Kept).
				Int("deleted", res.Deleted).
				Int("skipped", res.Skipped).
				Msg("auto-prune runs")
		}
	}

	runID, err := newRunID()
	if err != nil {
		return Result{}, err
	}
	log.Info().
		Str("run_id", runID).
		Str("goal", goal).
		Int("max_iterations", r.cfg.Budgets.MaxIterations).
		Int("ac_count", len(ac)).
		Msg("run started")
	runDir := filepath.Join(r.normaDir, "runs", runID)
	stepsDir := filepath.Join(runDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create run steps: %w", err)
	}
	if err := writeNormaMD(runDir, goal, ac, r.cfg.Budgets); err != nil {
		return Result{}, err
	}
	if err := r.store.CreateRun(ctx, runID, goal, runDir, 1); err != nil {
		return Result{}, err
	}

	iteration := 1
	stepIndex := 0
	artifacts := []string{}
	budgets := budgetsFromConfig(BudgetsConfig{
		MaxIterations:   r.cfg.Budgets.MaxIterations,
		MaxPatchKB:      r.cfg.Budgets.MaxPatchKB,
		MaxChangedFiles: r.cfg.Budgets.MaxChangedFiles,
		MaxRiskyFiles:   r.cfg.Budgets.MaxRiskyFiles,
	})

	for iteration <= r.cfg.Budgets.MaxIterations {
		stepIndex++
		log.Info().Str("run_id", runID).Str("role", "plan").Int("iteration", iteration).Int("step_index", stepIndex).Msg("step start")
		planRes, err := r.runStepWithRetries(ctx, runID, goal, ac, iteration, stepIndex, "plan", artifacts, runDir, stepsDir, budgets)
		if err != nil {
			return Result{RunID: runID}, err
		}
		artifacts = append(artifacts, stepArtifacts(planRes)...)
		if err := r.commitStep(ctx, runID, planRes, "running", nil); err != nil {
			return Result{RunID: runID}, err
		}
		if planRes.Status != "ok" {
			if err := r.failRun(ctx, runID, iteration, stepIndex, "plan step failed"); err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "failed"}, nil
		}

		stepIndex++
		log.Info().Str("run_id", runID).Str("role", "do").Int("iteration", iteration).Int("step_index", stepIndex).Msg("step start")
		doRes, err := r.runStepWithRetries(ctx, runID, goal, ac, iteration, stepIndex, "do", artifacts, runDir, stepsDir, budgets)
		if err != nil {
			return Result{RunID: runID}, err
		}
		artifacts = append(artifacts, stepArtifacts(doRes)...)
		if err := r.commitStep(ctx, runID, doRes, "running", nil); err != nil {
			return Result{RunID: runID}, err
		}
		if doRes.Status != "ok" {
			if err := r.failRun(ctx, runID, iteration, stepIndex, "do step failed"); err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "failed"}, nil
		}

		stepIndex++
		log.Info().Str("run_id", runID).Str("role", "check").Int("iteration", iteration).Int("step_index", stepIndex).Msg("step start")
		checkRes, err := r.runStepWithRetries(ctx, runID, goal, ac, iteration, stepIndex, "check", artifacts, runDir, stepsDir, budgets)
		if err != nil {
			return Result{RunID: runID}, err
		}
		artifacts = append(artifacts, stepArtifacts(checkRes)...)
		verdict := ""
		if checkRes.Verdict != nil {
			verdict = checkRes.Verdict.Verdict
		}
		if verdict != "" {
			verdictCopy := verdict
			status := "running"
			if verdict == "PASS" {
				status = "passed"
			}
			if err := r.commitStep(ctx, runID, checkRes, status, &verdictCopy); err != nil {
				return Result{RunID: runID}, err
			}
			if verdict == "PASS" {
				return Result{RunID: runID, Status: "passed", Verdict: &verdictCopy}, nil
			}
		} else {
			if err := r.commitStep(ctx, runID, checkRes, "running", nil); err != nil {
				return Result{RunID: runID}, err
			}
		}
		if checkRes.Status != "ok" {
			if err := r.failRun(ctx, runID, iteration, stepIndex, "check step failed"); err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "failed"}, nil
		}

		stepIndex++
		log.Info().Str("run_id", runID).Str("role", "act").Int("iteration", iteration).Int("step_index", stepIndex).Msg("step start")
		actRes, err := r.runStepWithRetries(ctx, runID, goal, ac, iteration, stepIndex, "act", artifacts, runDir, stepsDir, budgets)
		if err != nil {
			return Result{RunID: runID}, err
		}
		artifacts = append(artifacts, stepArtifacts(actRes)...)

		runStatus := "running"
		if actRes.Protocol == "budget_exceeded" {
			runStatus = "stopped"
		}
		if err := r.commitStep(ctx, runID, actRes, runStatus, nil); err != nil {
			return Result{RunID: runID}, err
		}
		if actRes.Protocol == "budget_exceeded" {
			if err := r.stopRun(ctx, runID, iteration, stepIndex, actRes.Summary); err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "stopped"}, nil
		}
		if actRes.Status != "ok" {
			if err := r.failRun(ctx, runID, iteration, stepIndex, "act step failed"); err != nil {
				return Result{RunID: runID}, err
			}
			return Result{RunID: runID, Status: "failed"}, nil
		}

		iteration++
	}

	if err := r.stopRun(ctx, runID, iteration-1, stepIndex, "max_iterations exceeded"); err != nil {
		return Result{RunID: runID}, err
	}
	return Result{RunID: runID, Status: "stopped"}, nil
}

func (r *Runner) runStep(ctx context.Context, runID, goal string, ac []model.AcceptanceCriterion, iteration, stepIndex int, role string, artifacts []string, runDir, stepsDir string) (stepResult, error) {
	req := model.AgentRequest{
		Version: 1,
		RunID:   runID,
		Step: model.StepInfo{
			Index:     stepIndex,
			Role:      role,
			Iteration: iteration,
		},
		Goal: goal,
		Norma: model.NormaInfo{
			AcceptanceCriteria: ac,
			Budgets: model.Budgets{
				MaxIterations:   r.cfg.Budgets.MaxIterations,
				MaxPatchKB:      r.cfg.Budgets.MaxPatchKB,
				MaxChangedFiles: r.cfg.Budgets.MaxChangedFiles,
				MaxRiskyFiles:   r.cfg.Budgets.MaxRiskyFiles,
			},
		},
		Paths: model.RequestPaths{
			RepoRoot: r.repoRoot,
			RunDir:   runDir,
			StepDir:  "",
		},
		Context: model.RequestContext{
			Artifacts: artifacts,
		},
	}
	return executeStep(ctx, r.agents[role], req, stepsDir)
}

const maxAgentRetries = 2

func (r *Runner) runStepWithRetries(ctx context.Context, runID, goal string, ac []model.AcceptanceCriterion, iteration, stepIndex int, role string, artifacts []string, runDir, stepsDir string, budgets Budgets) (stepResult, error) {
	attempts := maxAgentRetries + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		res, err := r.runStep(ctx, runID, goal, ac, iteration, stepIndex, role, artifacts, runDir, stepsDir)
		if err != nil {
			return res, err
		}
		if role == "act" && res.Status == "ok" && res.PatchPath != "" {
			beforeAfterHash, beforeAfterStatus, err := applyPatch(ctx, r.repoRoot, res.PatchPath, budgets)
			if err != nil {
				res.Status = "fail"
				if isBudgetError(err) {
					res.Protocol = "budget_exceeded"
				} else {
					res.Protocol = "patch_failed"
				}
				res.Summary = err.Error()
			} else if beforeAfterHash != "" || beforeAfterStatus != "" {
				_ = r.store.UpdateRun(ctx, runID, RunUpdate{
					CurrentStepIndex: stepIndex,
					Iteration:        iteration,
					Status:           "running",
					Verdict:          nil,
				}, &Event{Type: "patch_applied", Message: "patch applied", DataJSON: patchEventData(beforeAfterHash, beforeAfterStatus)})
			}
		}
		retryable := res.Status != "ok" && res.Protocol != ""
		if res.Protocol == "budget_exceeded" {
			retryable = false
		}
		lastAttempt := attempt == attempts
		if !retryable || lastAttempt || res.Status == "ok" {
			if err := finalizeStep(&res); err != nil {
				return res, err
			}
			return res, nil
		}
		_ = os.RemoveAll(res.TempDir)
		log.Debug().Str("run_id", runID).Str("role", role).Int("step_index", stepIndex).Int("attempt", attempt).Msg("retrying step")
	}
	return stepResult{}, fmt.Errorf("exhausted retries")
}

func (r *Runner) commitStep(ctx context.Context, runID string, res stepResult, runStatus string, verdict *string) error {
	dataJSON := stepEventData(res)
	events := []Event{{
		Type:     "step_committed",
		Message:  fmt.Sprintf("step %03d-%s committed", res.StepIndex, res.Role),
		DataJSON: dataJSON,
	}}
	if res.Role == "check" && res.Verdict != nil {
		verdictData, _ := json.Marshal(map[string]any{"verdict": res.Verdict.Verdict})
		events = append(events, Event{Type: "verdict", Message: "verdict recorded", DataJSON: string(verdictData)})
	}
	if res.Protocol != "" {
		events = append(events, Event{Type: "protocol_error", Message: res.Protocol, DataJSON: ""})
	}
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
	return r.store.CommitStep(ctx, step, events, update)
}

func (r *Runner) stopRun(ctx context.Context, runID string, iteration, stepIndex int, reason string) error {
	update := RunUpdate{
		CurrentStepIndex: stepIndex,
		Iteration:        iteration,
		Status:           "stopped",
		Verdict:          nil,
	}
	event := Event{Type: "run_stopped", Message: reason, DataJSON: ""}
	if err := r.store.UpdateRun(ctx, runID, update, &event); err != nil {
		return err
	}
	return nil
}

func (r *Runner) failRun(ctx context.Context, runID string, iteration, stepIndex int, reason string) error {
	update := RunUpdate{
		CurrentStepIndex: stepIndex,
		Iteration:        iteration,
		Status:           "failed",
		Verdict:          nil,
	}
	event := Event{Type: "run_failed", Message: reason, DataJSON: ""}
	if err := r.store.UpdateRun(ctx, runID, update, &event); err != nil {
		return err
	}
	return nil
}

func stepEventData(res stepResult) string {
	payload := map[string]any{
		"role":   res.Role,
		"status": res.Status,
		"dir":    res.FinalDir,
	}
	if res.Protocol != "" {
		payload["protocol_error"] = res.Protocol
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func stepArtifacts(res stepResult) []string {
	if res.FinalDir == "" {
		return nil
	}
	artifacts := make([]string, 0, 4)
	seen := map[string]struct{}{}
	addIfExists := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		if _, err := os.Stat(path); err == nil {
			artifacts = append(artifacts, path)
			seen[path] = struct{}{}
		}
	}
	if res.Response != nil {
		for _, rel := range res.Response.Files {
			if rel == "" {
				continue
			}
			path := filepath.Join(res.FinalDir, filepath.Clean(rel))
			addIfExists(path)
		}
	}
	if res.Role == "plan" {
		addIfExists(filepath.Join(res.FinalDir, "plan.md"))
	}
	if res.Role == "check" {
		addIfExists(filepath.Join(res.FinalDir, "verdict.json"))
		addIfExists(filepath.Join(res.FinalDir, "scorecard.md"))
	}
	if res.PatchPath != "" {
		addIfExists(filepath.Join(res.FinalDir, "patch.diff"))
	}
	return artifacts
}

func patchEventData(hashSnapshot, statusSnapshot string) string {
	payload := map[string]string{
		"hash":   hashSnapshot,
		"status": statusSnapshot,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

func writeNormaMD(runDir, goal string, ac []model.AcceptanceCriterion, budgets config.Budgets) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	var b strings.Builder
	b.WriteString("# norma run\n\n")
	b.WriteString("Goal: ")
	b.WriteString(goal)
	b.WriteString("\n\n")
	b.WriteString("Acceptance Criteria:\n")
	if len(ac) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, c := range ac {
		b.WriteString("- [")
		b.WriteString(c.ID)
		b.WriteString("] ")
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	b.WriteString("\nBudgets:\n")
	b.WriteString(fmt.Sprintf("- max_iterations: %d\n", budgets.MaxIterations))
	if budgets.MaxPatchKB > 0 {
		b.WriteString(fmt.Sprintf("- max_patch_kb: %d\n", budgets.MaxPatchKB))
	}
	if budgets.MaxChangedFiles > 0 {
		b.WriteString(fmt.Sprintf("- max_changed_files: %d\n", budgets.MaxChangedFiles))
	}
	if budgets.MaxRiskyFiles > 0 {
		b.WriteString(fmt.Sprintf("- max_risky_files: %d\n", budgets.MaxRiskyFiles))
	}
	path := filepath.Join(runDir, "norma.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write norma.md: %w", err)
	}
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
