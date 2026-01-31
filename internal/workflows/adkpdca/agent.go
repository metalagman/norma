package adkpdca

import (
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/metalagman/ainvoke/adk"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// NormaPDCAAgent is a custom ADK agent that orchestrates one iteration of the PDCA loop.
type NormaPDCAAgent struct {
	cfg        config.Config
	store      *db.Store
	tracker    task.Tracker
	runInput   workflows.RunInput
	stepIndex  *int // Shared step index across iterations
	baseBranch string
}

func NewNormaPDCAAgent(cfg config.Config, store *db.Store, tracker task.Tracker, runInput workflows.RunInput, stepIndex *int, baseBranch string) *NormaPDCAAgent {
	return &NormaPDCAAgent{
		cfg:        cfg,
		store:      store,
		tracker:    tracker,
	runInput:   runInput,
		stepIndex:  stepIndex,
		baseBranch: baseBranch,
	}
}

func (a *NormaPDCAAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		iteration, err := ctx.Session().State().Get("iteration")
		itNum, ok := iteration.(int)
		if err != nil || !ok {
			itNum = 1
		}

		log.Info().Int("iteration", itNum).Msg("ADK PDCA Agent: starting iteration")

		// 1. PLAN
		planRes, err := a.runStep(ctx, itNum, normaloop.RolePlan, yield)
		if err != nil {
			yield(nil, fmt.Errorf("plan step failed: %w", err))
			return
		}
		if planRes.Status != "ok" {
			log.Warn().Str("status", planRes.Status).Msg("plan step stopped")
			_ = ctx.Session().State().Set("stop", true)
			return
		}

		// 2. DO
		doRes, err := a.runStep(ctx, itNum, normaloop.RoleDo, yield)
		if err != nil {
			yield(nil, fmt.Errorf("do step failed: %w", err))
			return
		}
		if doRes.Status != "ok" {
			log.Warn().Str("status", doRes.Status).Msg("do step stopped")
			_ = ctx.Session().State().Set("stop", true)
			return
		}

		// 3. CHECK
		checkRes, err := a.runStep(ctx, itNum, normaloop.RoleCheck, yield)
		if err != nil {
			yield(nil, fmt.Errorf("check step failed: %w", err))
			return
		}
		if checkRes.Status != "ok" {
			log.Warn().Str("status", checkRes.Status).Msg("check step stopped")
			_ = ctx.Session().State().Set("stop", true)
			return
		}

		// 4. ACT
		actRes, err := a.runStep(ctx, itNum, normaloop.RoleAct, yield)
		if err != nil {
			yield(nil, fmt.Errorf("act step failed: %w", err))
			return
		}

		// Set verdict in session state for LoopAgent to check if it should continue
		if checkRes.Check != nil {
			_ = ctx.Session().State().Set("verdict", checkRes.Check.Verdict.Status)
		}

		if actRes.Act != nil {
			_ = ctx.Session().State().Set("decision", actRes.Act.Decision)
			if actRes.Act.Decision == "close" {
				_ = ctx.Session().State().Set("stop", true)
			}
		}

		// Increment iteration for next run
		_ = ctx.Session().State().Set("iteration", itNum+1)
	}
}

func (a *NormaPDCAAgent) runStep(ctx agent.InvocationContext, iteration int, roleName string, yield func(*session.Event, error) bool) (*models.AgentResponse, error) {
	*a.stepIndex++
	index := *a.stepIndex

	role := normaloop.GetRole(roleName)
	if role == nil {
		return nil, fmt.Errorf("unknown role %q", roleName)
	}

	req := a.baseRequest(iteration, index, roleName)
	
	// Enrich request based on role and current state
	state := a.getTaskState(ctx)
	switch roleName {
	case normaloop.RolePlan:
		req.Plan = &models.PlanInput{Task: models.IDInfo{ID: a.runInput.TaskID}}
	case normaloop.RoleDo:
		if state.Plan == nil {
			return nil, fmt.Errorf("missing plan for do step")
		}
		req.Do = &models.DoInput{
			WorkPlan:          state.Plan.WorkPlan,
			EffectiveCriteria: state.Plan.AcceptanceCriteria.Effective,
		}
	case normaloop.RoleCheck:
		if state.Plan == nil || state.Do == nil {
			return nil, fmt.Errorf("missing plan or do for check step")
		}
		req.Check = &models.CheckInput{
			WorkPlan:          state.Plan.WorkPlan,
			EffectiveCriteria: state.Plan.AcceptanceCriteria.Effective,
			DoExecution:       state.Do.Execution,
		}
	case normaloop.RoleAct:
		if state.Check == nil {
			return nil, fmt.Errorf("missing check verdict for act step")
		}
		req.Act = &models.ActInput{
			CheckVerdict:      state.Check.Verdict,
			AcceptanceResults: state.Check.AcceptanceResults,
		}
	}

	// Prepare step directory and workspace
	stepsDir := filepath.Join(a.runInput.RunDir, "steps")
	stepDirName := fmt.Sprintf("%03d-%s", index, roleName)
	stepDir := filepath.Join(stepsDir, stepDirName)
	if err := os.MkdirAll(filepath.Join(stepDir, "logs"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(stepDir, "artifacts"), 0o755); err != nil {
		return nil, err
	}

	workspaceDir := filepath.Join(stepDir, "workspace")
	branchName := fmt.Sprintf("norma/task/%s", a.runInput.TaskID)
	if _, err := git.MountWorktree(ctx, a.runInput.GitRoot, workspaceDir, branchName, a.baseBranch); err != nil {
		return nil, fmt.Errorf("mount worktree: %w", err)
	}
	defer func() {
		_ = git.RemoveWorktree(ctx, a.runInput.GitRoot, workspaceDir)
	}()

	progressPath := filepath.Join(stepDir, "artifacts", "progress.md")
	req.Paths = models.RequestPaths{
		WorkspaceDir: workspaceDir,
		RunDir:       stepDir,
		Progress:     progressPath,
	}

	// Reconstruct progress.md
	if err := a.reconstructProgress(stepDir, state); err != nil {
		return nil, err
	}

	// Write input.json
	inputData, _ := json.MarshalIndent(req, "", "  ")
	_ = os.WriteFile(filepath.Join(stepDir, "input.json"), inputData, 0o644)

	// Create ExecAgent for this step
	agentCfg := a.cfg.Agents[roleName]
	cmd, err := a.resolveCmd(agentCfg)
	if err != nil {
		return nil, err
	}

	prompt, _ := role.Prompt(req)
	input, _ := role.MapRequest(req)
	inputJSON, _ := json.Marshal(input)

	execAgent, err := adk.NewExecAgent(
		roleName,
		fmt.Sprintf("Norma %s agent", roleName),
		cmd,
		adk.WithExecAgentPrompt(prompt),
		adk.WithExecAgentInputSchema(role.InputSchema()),
		adk.WithExecAgentOutputSchema(role.OutputSchema()),
		adk.WithExecAgentRunDir(stepDir),
		adk.WithExecAgentStdout(os.Stdout),
		adk.WithExecAgentStderr(os.Stderr),
	)
	if err != nil {
		return nil, err
	}

	// Run ExecAgent
	invCtx := &stepInvocationContext{
		InvocationContext: ctx,
		userContent:       genai.NewContentFromText(string(inputJSON), genai.RoleUser),
	}

	startTime := time.Now()
	var lastOut []byte
	for ev, err := range execAgent.Run(invCtx) {
		if err != nil {
			return nil, err
		}
		if !yield(ev, nil) {
			return nil, fmt.Errorf("yield stopped")
		}
		if ev.Content != nil && len(ev.Content.Parts) > 0 {
			lastOut = []byte(ev.Content.Parts[0].Text)
		}
	}
	endTime := time.Now()

	// Parse response
	resp, err := role.MapResponse(lastOut)
	if err != nil {
		return nil, fmt.Errorf("map response: %w", err)
	}
	resp.Timing.WallTimeMS = time.Since(startTime).Milliseconds()

	// Persist output.json
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	_ = os.WriteFile(filepath.Join(stepDir, "output.json"), respJSON, 0o644)

	// Commit to DB
	stepRec := db.StepRecord{
		RunID:     a.runInput.RunID,
		StepIndex: index,
		Role:      roleName,
		Iteration: iteration,
		Status:    resp.Status,
		StepDir:   stepDir,
		StartedAt: startTime.UTC().Format(time.RFC3339),
		EndedAt:   endTime.UTC().Format(time.RFC3339),
		Summary:   resp.Summary.Text,
	}
	update := db.Update{
		CurrentStepIndex: index,
		Iteration:        iteration,
		Status:           "running",
	}
	_ = a.store.CommitStep(ctx, stepRec, nil, update)

	// Update Task State and Persist to Beads
	a.updateTaskState(ctx, &resp, roleName, iteration, index)

	return &resp, nil
}

func (a *NormaPDCAAgent) baseRequest(iteration, index int, role string) models.AgentRequest {
	return models.AgentRequest{
		Run: models.RunInfo{
			ID:        a.runInput.RunID,
			Iteration: iteration,
		},
		Task: models.TaskInfo{
			ID:                 a.runInput.TaskID,
			Title:              a.runInput.Goal,
			AcceptanceCriteria: a.runInput.AcceptanceCriteria,
		},
		Step: models.StepInfo{
			Index: index,
			Name:  role,
		},
		Budgets: models.Budgets{
			MaxIterations: a.cfg.Budgets.MaxIterations,
		},
		StopReasonsAllowed: []string{
			"budget_exceeded",
			"dependency_blocked",
			"verify_missing",
			"replan_required",
		},
	}
}

func (a *NormaPDCAAgent) getTaskState(ctx agent.InvocationContext) *models.TaskState {
	s, err := ctx.Session().State().Get("task_state")
	if err != nil {
		return &models.TaskState{}
	}
	return s.(*models.TaskState)
}

func (a *NormaPDCAAgent) updateTaskState(ctx agent.InvocationContext, resp *models.AgentResponse, role string, iteration, index int) {
	state := a.getTaskState(ctx)
	switch role {
	case normaloop.RolePlan:
		state.Plan = resp.Plan
	case normaloop.RoleDo:
		state.Do = resp.Do
	case normaloop.RoleCheck:
		state.Check = resp.Check
	case normaloop.RoleAct:
		state.Act = resp.Act
	}

	// Append to journal
	entry := models.JournalEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		StepIndex:  index,
		Role:       role,
		Status:     resp.Status,
		StopReason: resp.StopReason,
		Title:      resp.Progress.Title,
		Details:    resp.Progress.Details,
	}
	if entry.Title == "" {
		entry.Title = fmt.Sprintf("%s step completed", role)
	}
	state.Journal = append(state.Journal, entry)

	_ = ctx.Session().State().Set("task_state", state)

	// Persist to Beads
	data, _ := json.MarshalIndent(state, "", "  ")
	_ = a.tracker.SetNotes(ctx, a.runInput.TaskID, string(data))
}

func (a *NormaPDCAAgent) reconstructProgress(dir string, state *models.TaskState) error {
	path := filepath.Join(dir, "artifacts", "progress.md")
	var sb strings.Builder
	for _, entry := range state.Journal {
		sb.WriteString(fmt.Sprintf("## %s — %d %s — %s/%s\n", entry.Timestamp, entry.StepIndex, entry.Role, entry.Status, entry.StopReason))
		sb.WriteString(fmt.Sprintf("**Title:** %s\n\n", entry.Title))
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (a *NormaPDCAAgent) resolveCmd(cfg config.AgentConfig) ([]string, error) {
	if len(cfg.Cmd) > 0 {
		return cfg.Cmd, nil
	}
	// Fallback logic similar to resolveCmd in internal/agent/agent.go
	switch cfg.Type {
	case "claude":
		return []string{"claude"}, nil
	case "gemini":
		return []string{"gemini"}, nil
	case "codex":
		return []string{"codex", "exec"}, nil
	default:
		return nil, fmt.Errorf("cannot resolve command for agent type %q", cfg.Type)
	}
}

type stepInvocationContext struct {
	agent.InvocationContext
	userContent *genai.Content
}

func (m *stepInvocationContext) UserContent() *genai.Content { return m.userContent }
