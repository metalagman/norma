package normaloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
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

// Workflow implements the standard PDCA loop.
type Workflow struct {
	cfg     config.Config
	store   *db.Store
	tracker task.Tracker
	agents  map[string]agent.Runner
}

// NewWorkflow creates a new normaloop workflow.
func NewWorkflow(cfg config.Config, store *db.Store, tracker task.Tracker) (*Workflow, error) {
	agents := make(map[string]agent.Runner)
	for _, roleName := range []string{RolePlan, RoleDo, RoleCheck, RoleAct} {
		role := GetRole(roleName)
		if role == nil {
			return nil, fmt.Errorf("unknown role %q", roleName)
		}

		var roleRunner agent.Runner
		if existing, ok := role.Runner().(agent.Runner); ok && existing != nil {
			roleRunner = existing
		} else {
			agentCfg, ok := cfg.Agents[roleName]
			if !ok {
				return nil, fmt.Errorf("missing agent config for role %q", roleName)
			}
			var err error
			roleRunner, err = agent.NewRunner(agentCfg, role)
			if err != nil {
				return nil, fmt.Errorf("init %s agent: %w", roleName, err)
			}
			role.SetRunner(roleRunner)
		}
		agents[roleName] = roleRunner
	}

	return &Workflow{
		cfg:     cfg,
		store:   store,
		tracker: tracker,
		agents:  agents,
	}, nil
}

func (w *Workflow) Name() string {
	return "normaloop"
}

func (w *Workflow) Run(ctx context.Context, input workflows.RunInput) (workflows.RunResult, error) {
	stepsDir := filepath.Join(input.RunDir, "steps")
	if err := os.MkdirAll(stepsDir, 0o755); err != nil {
		return workflows.RunResult{}, fmt.Errorf("create run steps: %w", err)
	}

	taskItem, err := w.tracker.Task(ctx, input.TaskID)
	if err != nil {
		return workflows.RunResult{}, fmt.Errorf("get task: %w", err)
	}

	state := models.TaskState{}
	if taskItem.Notes != "" {
		if err := json.Unmarshal([]byte(taskItem.Notes), &state); err == nil {
			log.Info().Str("task_id", input.TaskID).Msg("loaded existing state from task notes")
		}
	}

	iteration := 1
	stepIndex := 0

	var lastPlan *models.PlanOutput
	var lastDo *models.DoOutput
	var lastCheck *models.CheckOutput
	var lastAct *models.ActOutput

	lastPlan = state.Plan
	lastDo = state.Do
	lastCheck = state.Check

	hasLabel := func(name string) bool {
		for _, l := range taskItem.Labels {
			if l == name {
				return true
			}
		}
		return false
	}

	for iteration <= w.cfg.Budgets.MaxIterations {
		log.Info().Int("iteration", iteration).Msg("starting iteration")
		// 1. PLAN
	skipPlan := false
		if iteration == 1 && hasLabel(labelHasPlan) && lastPlan != nil {
			log.Info().Str("task_id", input.TaskID).Msg("skipping plan: norma-has-plan label present")
			skipPlan = true
		} else if iteration > 1 && lastAct != nil && lastAct.Decision == "continue" && lastPlan != nil {
			log.Info().Str("task_id", input.TaskID).Msg("skipping plan: Act decision was 'continue'")
			skipPlan = true
		}

		if !skipPlan {
			log.Info().Msg("executing plan step")
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasPlan)
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasDo)
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasCheck)

			stepIndex++
			if err := w.tracker.MarkStatus(ctx, input.TaskID, "planning"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to planning")
			}
			planReq := w.baseRequest(input, iteration, stepIndex, RolePlan)
			planReq.Plan = &models.PlanInput{Task: models.IDInfo{ID: input.TaskID}}

			planRes, err := w.runAndCommitStep(ctx, planReq, stepsDir, &state, input.GitRoot, input.BaseBranch)
			if err != nil {
				log.Error().Err(err).Msg("plan step execution failed with error")
				return workflows.RunResult{}, fmt.Errorf("execute plan step: %w", err)
			}
			if planRes.Status != statusOK && (planRes.Response == nil || planRes.Response.Plan == nil) {
				log.Warn().Str("status", planRes.Status).Msg("plan step failed without required data, stopping")
				return w.handleStop(ctx, input.RunID, iteration, stepIndex, planRes, input.TaskID)
			}
			lastPlan = planRes.Response.Plan

			// Persist plan
			state.Plan = lastPlan
			if err := w.persistState(ctx, input.TaskID, state); err != nil {
				return workflows.RunResult{}, fmt.Errorf("persist state after plan: %w", err)
			}
			_ = w.tracker.AddLabel(ctx, input.TaskID, labelHasPlan)
		}

		// 2. DO
		if iteration == 1 && hasLabel(labelHasDo) && lastDo != nil {
			log.Info().Str("task_id", input.TaskID).Msg("skipping do: norma-has-do label present")
		} else {
			log.Info().Msg("executing do step")
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasDo)
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasCheck)

			stepIndex++
			if err := w.tracker.MarkStatus(ctx, input.TaskID, "doing"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to doing")
			}
			doReq := w.baseRequest(input, iteration, stepIndex, RoleDo)
			doReq.Do = &models.DoInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
			}

			doRes, err := w.runAndCommitStep(ctx, doReq, stepsDir, &state, input.GitRoot, input.BaseBranch)
			if err != nil {
				log.Error().Err(err).Msg("do step execution failed with error")
				return workflows.RunResult{}, fmt.Errorf("execute do step: %w", err)
			}
			if doRes.Status != statusOK && (doRes.Response == nil || doRes.Response.Do == nil) {
				log.Warn().Str("status", doRes.Status).Msg("do step failed without required data, stopping")
				return w.handleStop(ctx, input.RunID, iteration, stepIndex, doRes, input.TaskID)
			}

			lastDo = doRes.Response.Do

			// Persist do
			state.Do = lastDo
			if err := w.persistState(ctx, input.TaskID, state); err != nil {
				return workflows.RunResult{}, fmt.Errorf("persist state after do: %w", err)
			}
			_ = w.tracker.AddLabel(ctx, input.TaskID, labelHasDo)
		}

		// 3. CHECK
		if iteration == 1 && hasLabel(labelHasCheck) && lastCheck != nil {
			log.Info().Str("task_id", input.TaskID).Msg("skipping check: norma-has-check label present")
		} else {
			log.Info().Msg("executing check step")
			_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasCheck)

			stepIndex++
			if err := w.tracker.MarkStatus(ctx, input.TaskID, "checking"); err != nil {
				log.Warn().Err(err).Msg("failed to update task status to checking")
			}
			checkReq := w.baseRequest(input, iteration, stepIndex, RoleCheck)
			checkReq.Check = &models.CheckInput{
				WorkPlan:          lastPlan.WorkPlan,
				EffectiveCriteria: lastPlan.AcceptanceCriteria.Effective,
				DoExecution:       lastDo.Execution,
			}

			checkRes, err := w.runAndCommitStep(ctx, checkReq, stepsDir, &state, input.GitRoot, input.BaseBranch)
			if err != nil {
				log.Error().Err(err).Msg("check step execution failed with error")
				return workflows.RunResult{}, fmt.Errorf("execute check step: %w", err)
			}
			if checkRes.Status != statusOK && (checkRes.Response == nil || checkRes.Response.Check == nil) {
				log.Warn().Str("status", checkRes.Status).Msg("check step failed without required data, stopping")
				return w.handleStop(ctx, input.RunID, iteration, stepIndex, checkRes, input.TaskID)
			}

			if checkRes.Response == nil || checkRes.Response.Check == nil {
				log.Error().Str("status", checkRes.Status).Msg("check step finished but Response.Check is nil")
				return w.failRun(ctx, input.RunID, iteration, stepIndex, "check step produced no verdict data", input.TaskID)
			}

			lastCheck = checkRes.Response.Check

			// Persist check
			state.Check = lastCheck
			if err := w.persistState(ctx, input.TaskID, state); err != nil {
				return workflows.RunResult{}, fmt.Errorf("persist state after check: %w", err)
			}
			_ = w.tracker.AddLabel(ctx, input.TaskID, labelHasCheck)
		}

		// 4. ACT
		log.Info().Msg("preparing act step")
		if lastCheck == nil {
			log.Error().Msg("lastCheck is nil before ACT, this should not happen")
			return w.failRun(ctx, input.RunID, iteration, stepIndex, "internal error: missing check verdict for act", input.TaskID)
		}

		stepIndex++
		if err := w.tracker.MarkStatus(ctx, input.TaskID, "acting"); err != nil {
			log.Warn().Err(err).Msg("failed to update task status to acting")
		}
		actReq := w.baseRequest(input, iteration, stepIndex, RoleAct)
		actReq.Act = &models.ActInput{
			CheckVerdict:      lastCheck.Verdict,
			AcceptanceResults: lastCheck.AcceptanceResults,
		}

		actRes, err := w.runAndCommitStep(ctx, actReq, stepsDir, &state, input.GitRoot, input.BaseBranch)
		if err != nil {
			log.Error().Err(err).Msg("act step execution failed with error")
			return workflows.RunResult{}, fmt.Errorf("execute act step: %w", err)
		}

		if actRes.Response != nil && actRes.Response.Act != nil {
			lastAct = actRes.Response.Act
			state.Act = lastAct
			if lastAct.Decision == "replan" {
				log.Info().Msg("act decision is replan, clearing has-plan label")
				_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasPlan)
				_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasDo)
				_ = w.tracker.RemoveLabel(ctx, input.TaskID, labelHasCheck)
				lastPlan = nil
				state.Plan = nil
				if err := w.persistState(ctx, input.TaskID, state); err != nil {
					return workflows.RunResult{}, fmt.Errorf("persist state after act: %w", err)
				}
			}
		}

		log.Info().Str("verdict", lastCheck.Verdict.Status).Msg("evaluating verdict")
		if lastCheck.Verdict.Status == "PASS" {
			log.Info().Msg("verdict is PASS")
			verdict := "PASS"
			return workflows.RunResult{Status: statusPassed, Verdict: &verdict}, nil
		}

		log.Info().Str("act_status", actRes.Status).Msg("evaluating act decision")
		if actRes.Status == statusStop || actRes.Status == statusError || (actRes.Response != nil && actRes.Response.Act != nil && actRes.Response.Act.Decision == "close") {
			log.Info().Msg("act decision is stop or close, stopping run")
			return w.handleStop(ctx, input.RunID, iteration, stepIndex, actRes, input.TaskID)
		}

		log.Info().Msg("continuing to next iteration")
		iteration++
	}

	return workflows.RunResult{Status: statusStopped}, nil
}

func (w *Workflow) baseRequest(input workflows.RunInput, iteration, index int, role string) models.AgentRequest {
	return models.AgentRequest{
		Run: models.RunInfo{
			ID:        input.RunID,
			Iteration: iteration,
		},
		Task: models.TaskInfo{
			ID:                 input.TaskID,
			Title:              input.Goal,
			AcceptanceCriteria: input.AcceptanceCriteria,
		},
		Step: models.StepInfo{
			Index: index,
			Name:  role,
		},
		Paths: models.RequestPaths{},
		Budgets: models.Budgets{
			MaxIterations: w.cfg.Budgets.MaxIterations,
		},
		StopReasonsAllowed: []string{
			"budget_exceeded",
			"dependency_blocked",
			"verify_missing",
			"replan_required",
		},
		Context: models.RequestContext{
			Facts: make(map[string]any),
		},
	}
}

type stepResult struct {
	Role      string
	Iteration int
	StepIndex int
	Status    string
	Protocol  string
	StartedAt time.Time
	EndedAt   time.Time
	FinalDir  string
	Summary   string
	Response  *models.AgentResponse
}

var ErrRetryable = errors.New("retryable error")

func (w *Workflow) runAndCommitStep(ctx context.Context, req models.AgentRequest, stepsDir string, state *models.TaskState, gitRoot, baseBranch string) (stepResult, error) {
	maxAttempts := 3
	var lastRes stepResult
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req.Context.Attempt = attempt
		res, err := w.executeStep(ctx, req, stepsDir, state, gitRoot, baseBranch)
		lastRes = res
		lastErr = err

		if err == nil {
			err = w.commitStep(ctx, req.Run.ID, res, statusRunning, nil)
			if err != nil {
				return res, fmt.Errorf("commit step: %w", err)
			}
			w.appendToProgress(res, req.Task.ID, state)
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

		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(time.Second * time.Duration(attempt+1)):
		}
	}

	if errors.Is(lastErr, ErrRetryable) {
		_ = w.commitStep(ctx, req.Run.ID, lastRes, statusRunning, nil)
		w.appendToProgress(lastRes, req.Task.ID, state)
	}

	return lastRes, lastErr
}

func (w *Workflow) executeStep(ctx context.Context, req models.AgentRequest, stepsDir string, state *models.TaskState, gitRoot, baseBranch string) (stepResult, error) {
	startedAt := time.Now()
	roleName := req.Step.Name
	stepDirName := fmt.Sprintf("%03d-%s", req.Step.Index, roleName)
	stepDir := filepath.Join(stepsDir, stepDirName)

	if err := os.MkdirAll(stepDir, 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create step dir: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(stepDir, "logs"), 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create logs dir: %w", err)
	}

	// Prepare paths for agent
	workspaceDir := filepath.Join(stepDir, "workspace")
	branchName := fmt.Sprintf("norma/task/%s", req.Task.ID)
	if _, err := git.MountWorktree(ctx, gitRoot, workspaceDir, branchName, baseBranch); err != nil {
		return stepResult{}, fmt.Errorf("mount worktree: %w", err)
	}
	defer func() {
		if err := git.RemoveWorktree(ctx, gitRoot, workspaceDir); err != nil {
			log.Warn().Err(err).Msg("failed to remove worktree")
		}
	}()

	progressPath := filepath.Join(stepDir, "artifacts", "progress.md")
	req.Paths = models.RequestPaths{
		WorkspaceDir: workspaceDir,
		RunDir:       stepDir,
		Progress:     progressPath,
	}

	// Prepare artifacts dir and progress.md
	if err := os.MkdirAll(filepath.Join(stepDir, "artifacts"), 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create artifacts dir: %w", err)
	}
	if err := w.reconstructProgress(stepDir, req.Task.ID, state); err != nil {
		return stepResult{}, fmt.Errorf("reconstruct progress: %w", err)
	}

	// Prepare input.json
	inputPath := filepath.Join(stepDir, "input.json")
	inputData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return stepResult{}, fmt.Errorf("marshal input request: %w", err)
	}
	if err := os.WriteFile(inputPath, inputData, 0o644); err != nil {
		return stepResult{}, fmt.Errorf("write input.json: %w", err)
	}

	// Run agent
	agentRunner := w.agents[roleName]

	var stdoutBuf, stderrBuf bytes.Buffer
	multiStdout := io.MultiWriter(os.Stdout, &stdoutBuf)
	multiStderr := io.MultiWriter(os.Stderr, &stderrBuf)

	agentOut, _, exitCode, err := agentRunner.Run(ctx, req, multiStdout, multiStderr)
	endedAt := time.Now()

	res := stepResult{
		Role:      roleName,
		Iteration: req.Run.Iteration,
		StepIndex: req.Step.Index,
		StartedAt: startedAt,
		EndedAt:   endedAt,
		FinalDir:  stepDir,
	}

	if err != nil {
		res.Status = statusError
		res.Summary = fmt.Sprintf("exit code %d: %v", exitCode, err)
		return res, fmt.Errorf("agent run: %w", err)
	}

	// Capture logs to files
	if err := os.WriteFile(filepath.Join(stepDir, "logs", "stdout.txt"), stdoutBuf.Bytes(), 0o644); err != nil {
		log.Warn().Err(err).Msg("failed to write stdout log")
	}
	if err := os.WriteFile(filepath.Join(stepDir, "logs", "stderr.txt"), stderrBuf.Bytes(), 0o644); err != nil {
		log.Warn().Err(err).Msg("failed to write stderr log")
	}

	// Parse AgentResponse
	var agentResp models.AgentResponse
	parsed := false

	// 1. Try reading output.json from step dir first
	outputPath := filepath.Join(stepDir, "output.json")
	if data, err := os.ReadFile(outputPath); err == nil {
		if err := json.Unmarshal(data, &agentResp); err == nil {
			parsed = true
			log.Debug().Str("role", roleName).Msg("using output.json from step directory")
		} else {
			// Try extraction even from the file if it's messy
			if recovered, ok := extractJSON(data); ok {
				if err := json.Unmarshal(recovered, &agentResp); err == nil {
					parsed = true
					log.Debug().Str("role", roleName).Msg("using extracted JSON from output.json")
				}
			}
		}
	}

	// 2. Fallback to stdout if output.json is missing or invalid
	if !parsed {
		if err := json.Unmarshal(agentOut, &agentResp); err == nil {
			parsed = true
		} else {
			recovered, ok := extractJSON(agentOut)
			if ok {
				if err := json.Unmarshal(recovered, &agentResp); err == nil {
					parsed = true
				}
			}
		}
	}

	if !parsed {
		res.Status = statusError
		res.Summary = "failed to parse agent response: no valid JSON found in output.json or stdout"
		return res, fmt.Errorf("parse agent response: invalid format")
	}

	res.Status = agentResp.Status
	res.Protocol = agentResp.StopReason
	res.Summary = agentResp.Summary.Text
	res.Response = &agentResp

	// Ensure output.json exists and is fresh with the parsed response
	if !parsed {
		// This should not be reached due to the !parsed check above
	} else {
		data, _ := json.MarshalIndent(agentResp, "", "  ")
		_ = os.WriteFile(outputPath, data, 0o644)
	}

	return res, nil
}

func extractJSON(data []byte) ([]byte, bool) {
	start := bytes.IndexByte(data, '{')
	end := bytes.LastIndexByte(data, '}')
	if start == -1 || end == -1 || start >= end {
		return nil, false
	}
	return data[start : end+1], true
}

func (w *Workflow) commitStep(ctx context.Context, runID string, res stepResult, runStatus string, verdict *string) error {
	step := db.StepRecord{
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
	update := db.Update{
		CurrentStepIndex: res.StepIndex,
		Iteration:        res.Iteration,
		Status:           runStatus,
		Verdict:          verdict,
	}
	return w.store.CommitStep(ctx, step, nil, update)
}

func (w *Workflow) handleStop(ctx context.Context, runID string, iteration, index int, res stepResult, taskID string) (workflows.RunResult, error) {
	status := statusFailed
	if res.Status == statusStop {
		status = statusStopped
	}
	if err := w.store.UpdateRun(ctx, runID, db.Update{
		Status:           status,
		Iteration:        iteration,
		CurrentStepIndex: index,
	}, nil); err != nil {
		log.Warn().Err(err).Msg("failed to update run status")
	}

	// Update task status in Beads
	if err := w.tracker.MarkStatus(ctx, taskID, status); err != nil {
		log.Warn().Err(err).Msg("failed to update task status in beads")
	}

	return workflows.RunResult{Status: status}, nil
}

func (w *Workflow) failRun(ctx context.Context, runID string, iteration, stepIndex int, reason string, taskID string) (workflows.RunResult, error) {
	update := db.Update{
		CurrentStepIndex: stepIndex,
		Iteration:        iteration,
		Status:           statusFailed,
		Verdict:          nil,
	}
	event := db.Event{Type: "run_failed", Message: reason, DataJSON: ""}
	if err := w.store.UpdateRun(ctx, runID, update, &event); err != nil {
		return workflows.RunResult{}, fmt.Errorf("update run: %w", err)
	}

	// Update task status in Beads
	if err := w.tracker.MarkStatus(ctx, taskID, statusFailed); err != nil {
		log.Warn().Err(err).Msg("failed to update task status in beads")
	}

	return workflows.RunResult{Status: statusFailed}, nil
}

func (w *Workflow) persistState(ctx context.Context, taskID string, state models.TaskState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task state: %w", err)
	}
	if err := w.tracker.SetNotes(ctx, taskID, string(data)); err != nil {
		return fmt.Errorf("set task notes: %w", err)
	}
	return nil
}

func (w *Workflow) appendToProgress(res stepResult, taskID string, state *models.TaskState) {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	stopReason := res.Protocol
	if res.Response != nil && res.Response.StopReason != "" {
		stopReason = res.Response.StopReason
	}
	if stopReason == "" {
		stopReason = "none"
	}

	entry := models.JournalEntry{
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

	state.Journal = append(state.Journal, entry)
}

func (w *Workflow) reconstructProgress(dir string, taskID string, state *models.TaskState) error {
	path := filepath.Join(dir, "artifacts", "progress.md")
	var b strings.Builder
	for _, entry := range state.Journal {
		b.WriteString(fmt.Sprintf("## %s — %d %s — %s/%s\n", entry.Timestamp, entry.StepIndex, strings.ToUpper(entry.Role), entry.Status, entry.StopReason))
		b.WriteString(fmt.Sprintf("**Task:** %s  \n", taskID))
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
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write progress.md: %w", err)
	}
	return nil
}