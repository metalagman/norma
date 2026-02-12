package adkpdca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/metalagman/ainvoke/adk"
	normaagent "github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/db"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// NormaPDCAAgent is a custom ADK agent that orchestrates one iteration of the PDCA loop.
type NormaPDCAAgent struct {
	cfg            config.Config
	store          *db.Store
	tracker        task.Tracker
	runInput       workflows.RunInput
	stepIndex      *int // Shared step index across iterations
	baseBranch     string
	sessionService session.Service

	planAgent  agent.Agent
	doAgent    agent.Agent
	checkAgent agent.Agent
	actAgent   agent.Agent
}

// NewNormaPDCAAgent creates and configures the entire custom agent workflow.
func NewNormaPDCAAgent(cfg config.Config, store *db.Store, tracker task.Tracker, runInput workflows.RunInput, stepIndex *int, baseBranch string, sessionService session.Service) (agent.Agent, []agent.Agent, error) {
	orchestrator := &NormaPDCAAgent{
		cfg:            cfg,
		store:          store,
		tracker:        tracker,
		runInput:       runInput,
		stepIndex:      stepIndex,
		baseBranch:     baseBranch,
		sessionService: sessionService,
	}

	orchestrator.planAgent = orchestrator.createSubAgent(normaloop.RolePlan)
	orchestrator.doAgent = orchestrator.createSubAgent(normaloop.RoleDo)
	orchestrator.checkAgent = orchestrator.createSubAgent(normaloop.RoleCheck)
	orchestrator.actAgent = orchestrator.createSubAgent(normaloop.RoleAct)

	subAgents := []agent.Agent{orchestrator.planAgent, orchestrator.doAgent, orchestrator.checkAgent, orchestrator.actAgent}

	ag, err := agent.New(agent.Config{
		Name:        "NormaPDCAAgent",
		Description: "Orchestrates story generation, critique, revision, and checks.",
		Run:         orchestrator.Run,
	})
	return ag, subAgents, err
}

func (a *NormaPDCAAgent) createSubAgent(roleName string) agent.Agent {
	ag, _ := agent.New(agent.Config{
		Name:        roleName,
		Description: fmt.Sprintf("Norma %s agent", roleName),
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				iteration, err := ctx.Session().State().Get("iteration")
				itNum, ok := iteration.(int)
				if err != nil || !ok {
					itNum = 1
				}

				log.Info().Str("role", roleName).Int("iteration", itNum).Msg("ADK PDCA Sub-agent: starting step")
				resp, err := a.runStep(ctx, itNum, roleName, yield)
				if err != nil {
					log.Error().Err(err).Str("role", roleName).Msg("ADK PDCA Sub-agent: step failed")
					yield(nil, err)
					return
				}
				if err := validateStepResponse(roleName, resp); err != nil {
					log.Error().Err(err).Str("role", roleName).Msg("ADK PDCA Sub-agent: invalid step response")
					yield(nil, err)
					return
				}

				log.Debug().Str("role", roleName).Str("status", resp.Status).Msg("ADK PDCA Sub-agent: step completed")

				// Communicate results via session state
				if roleName == normaloop.RoleCheck && resp.Check != nil {
					log.Debug().Str("verdict", resp.Check.Verdict.Status).Msg("ADK PDCA Sub-agent: setting check verdict in state")
					_ = ctx.Session().State().Set("verdict", resp.Check.Verdict.Status)
				}
				if roleName == normaloop.RoleAct && resp.Act != nil {
					log.Debug().Str("decision", resp.Act.Decision).Msg("ADK PDCA Sub-agent: setting act decision in state")
					_ = ctx.Session().State().Set("decision", resp.Act.Decision)
					if resp.Act.Decision == "close" {
						log.Info().Msg("ADK PDCA Sub-agent: act decision is close, stopping loop")
						_ = ctx.Session().State().Set("stop", true)
					}
				}
				if resp.Status != "ok" {
					log.Warn().Str("role", roleName).Str("status", resp.Status).Msg("ADK PDCA Sub-agent: non-ok status, stopping loop")
					_ = ctx.Session().State().Set("stop", true)
				}
			}
		},
	})
	return ag
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
		log.Debug().Msg("ADK PDCA Agent: invoking plan agent")
		for event, err := range a.planAgent.Run(ctx) {
			if !yield(event, err) {
				return
			}
		}
		if a.shouldStop(ctx) {
			log.Info().Msg("ADK PDCA Agent: stopping after Plan step")
			return
		}

		// 2. DO
		log.Debug().Msg("ADK PDCA Agent: invoking do agent")
		for event, err := range a.doAgent.Run(ctx) {
			if !yield(event, err) {
				return
			}
		}
		if a.shouldStop(ctx) {
			log.Info().Msg("ADK PDCA Agent: stopping after Do step")
			return
		}

		// 3. CHECK
		log.Debug().Msg("ADK PDCA Agent: invoking check agent")
		for event, err := range a.checkAgent.Run(ctx) {
			if !yield(event, err) {
				return
			}
		}
		if a.shouldStop(ctx) {
			log.Info().Msg("ADK PDCA Agent: stopping after Check step")
			return
		}

		// 4. ACT
		log.Debug().Msg("ADK PDCA Agent: invoking act agent")
		for event, err := range a.actAgent.Run(ctx) {
			if !yield(event, err) {
				return
			}
		}

		// Increment iteration for next run
		log.Info().Int("iteration", itNum).Msg("ADK PDCA Agent: iteration finished")
		_ = ctx.Session().State().Set("iteration", itNum+1)
	}
}

func (a *NormaPDCAAgent) shouldStop(ctx agent.InvocationContext) bool {
	stop, err := ctx.Session().State().Get("stop")
	if err == nil {
		if s, ok := stop.(bool); ok && s {
			return true
		}
	}
	return false
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
	log.Debug().Str("workspace", workspaceDir).Str("branch", branchName).Msg("ADK PDCA Agent: mounting worktree")
	if _, err := git.MountWorktree(ctx, a.runInput.GitRoot, workspaceDir, branchName, a.baseBranch); err != nil {
		return nil, fmt.Errorf("mount worktree: %w", err)
	}
	defer func() {
		log.Debug().Str("workspace", workspaceDir).Msg("ADK PDCA Agent: removing worktree")
		_ = git.RemoveWorktree(ctx, a.runInput.GitRoot, workspaceDir)
	}()

	progressPath, _ := filepath.Abs(filepath.Join(stepDir, "artifacts", "progress.md"))
	absStepDir, _ := filepath.Abs(stepDir)
	absWorkspaceDir, _ := filepath.Abs(workspaceDir)

	req.Paths = models.RequestPaths{
		WorkspaceDir: absWorkspaceDir,
		RunDir:       absStepDir,
		Progress:     progressPath,
	}

	// Reconstruct progress.md
	if err := a.reconstructProgress(stepDir, state); err != nil {
		return nil, err
	}

	// Create input.json
	inputData, _ := json.MarshalIndent(req, "", "  ")
	_ = os.WriteFile(filepath.Join(stepDir, "input.json"), inputData, 0o644)

	// Create ExecAgent for this step
	agentCfg := a.cfg.Agents[roleName]
	cmd, err := normaagent.ResolveCmd(agentCfg)
	if err != nil {
		return nil, err
	}

	log.Debug().Str("role", roleName).Interface("cmd", cmd).Msg("ADK PDCA Agent: creating ExecAgent")
	prompt, _ := role.Prompt(req)

	// Save prompt to logs/prompt.txt
	promptPath := filepath.Join(stepDir, "logs", "prompt.txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		log.Warn().Err(err).Str("path", promptPath).Msg("failed to save prompt log")
	}

	input, _ := role.MapRequest(req)
	inputJSON, _ := json.Marshal(input)

	// Prepare log files
	stdoutFile, err := os.OpenFile(filepath.Join(stepDir, "logs", "stdout.txt"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Warn().Err(err).Msg("failed to create stdout log file")
	}
	defer func() { _ = stdoutFile.Close() }()

	stderrFile, err := os.OpenFile(filepath.Join(stepDir, "logs", "stderr.txt"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Warn().Err(err).Msg("failed to create stderr log file")
	}
	defer func() { _ = stderrFile.Close() }()

	var multiStdout, multiStderr io.Writer
	if stdoutFile != nil {
		multiStdout = io.MultiWriter(os.Stdout, stdoutFile)
	} else {
		multiStdout = os.Stdout
	}
	if stderrFile != nil {
		multiStderr = io.MultiWriter(os.Stderr, stderrFile)
	} else {
		multiStderr = os.Stderr
	}

	execAgent, err := adk.NewExecAgent(
		roleName,
		fmt.Sprintf("Norma %s agent", roleName),
		cmd,
		adk.WithExecAgentPrompt(prompt),
		adk.WithExecAgentInputSchema(role.InputSchema()),
		adk.WithExecAgentOutputSchema(role.OutputSchema()),
		adk.WithExecAgentRunDir(stepDir),
		adk.WithExecAgentStdout(multiStdout),
		adk.WithExecAgentStderr(multiStderr),
	)
	if err != nil {
		return nil, err
	}

	// Create ADK Runner for the sub-agent
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma",
		Agent:          execAgent,
		SessionService: a.sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create adk runner for sub-agent: %w", err)
	}

	// Run ExecAgent
	userContent := genai.NewContentFromText(string(inputJSON), genai.RoleUser)
	userID := "norma-user"

	startTime := time.Now()
	var lastOut []byte
	for ev, err := range adkRunner.Run(ctx, userID, ctx.Session().ID(), userContent, agent.RunConfig{}) {
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

	// Persist Do workspace changes before worktree cleanup.
	if roleName == normaloop.RoleDo && resp.Status == "ok" {
		if err := commitWorkspaceChanges(ctx, workspaceDir, a.runInput.RunID, a.runInput.TaskID, index); err != nil {
			return nil, err
		}
	}

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

	// Update Task State and persist to Beads.
	if err := a.updateTaskState(ctx, &resp, roleName, iteration, index); err != nil {
		return nil, err
	}

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

func validateStepResponse(roleName string, resp *models.AgentResponse) error {
	if resp == nil {
		return fmt.Errorf("nil response for role %q", roleName)
	}

	switch roleName {
	case normaloop.RolePlan:
		switch resp.Status {
		case "ok":
			if resp.Plan == nil {
				return fmt.Errorf("plan step returned status ok without plan output")
			}
		case "stop":
			return nil
		default:
			return fmt.Errorf("plan step returned non-ok status %q", resp.Status)
		}
	case normaloop.RoleDo:
		switch resp.Status {
		case "ok":
			if resp.Do == nil {
				return fmt.Errorf("do step returned status ok without do output")
			}
		case "stop":
			return nil
		default:
			return fmt.Errorf("do step returned non-ok status %q", resp.Status)
		}
	}

	return nil
}

func (a *NormaPDCAAgent) getTaskState(ctx agent.InvocationContext) *models.TaskState {
	s, err := ctx.Session().State().Get("task_state")
	if err != nil {
		return &models.TaskState{}
	}
	return s.(*models.TaskState)
}

func (a *NormaPDCAAgent) updateTaskState(ctx agent.InvocationContext, resp *models.AgentResponse, role string, iteration, index int) error {
	if resp == nil {
		return fmt.Errorf("nil agent response for role %q", role)
	}

	state := a.getTaskState(ctx)
	applyAgentResponseToTaskState(state, resp, role, a.runInput.RunID, iteration, index, time.Now())

	if err := ctx.Session().State().Set("task_state", state); err != nil {
		return fmt.Errorf("set task state in session: %w", err)
	}

	// Persist to Beads
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task state: %w", err)
	}
	if err := a.tracker.SetNotes(ctx, a.runInput.TaskID, string(data)); err != nil {
		return fmt.Errorf("persist task state to task notes: %w", err)
	}

	return nil
}

func applyAgentResponseToTaskState(state *models.TaskState, resp *models.AgentResponse, role, runID string, iteration, index int, now time.Time) {
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

	entry := models.JournalEntry{
		Timestamp:  now.UTC().Format(time.RFC3339),
		RunID:      runID,
		Iteration:  iteration,
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
}

func commitWorkspaceChanges(ctx context.Context, workspaceDir, runID, taskID string, stepIndex int) error {
	status := strings.TrimSpace(git.RunCmd(ctx, workspaceDir, "git", "status", "--porcelain"))
	if status == "" {
		return nil
	}

	if err := git.RunCmdErr(ctx, workspaceDir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("stage workspace changes: %w", err)
	}

	commitMsg := fmt.Sprintf("chore: do step %03d\n\nRun: %s\nTask: %s", stepIndex, runID, taskID)
	if err := git.RunCmdErr(ctx, workspaceDir, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("commit workspace changes: %w", err)
	}

	return nil
}

func (a *NormaPDCAAgent) reconstructProgress(dir string, state *models.TaskState) error {
	path := filepath.Join(dir, "artifacts", "progress.md")
	var sb strings.Builder
	for _, entry := range state.Journal {
		stopReason := entry.StopReason
		if stopReason == "" {
			stopReason = "none"
		}
		title := entry.Title
		if title == "" {
			title = fmt.Sprintf("%s step completed", entry.Role)
		}
		runID := entry.RunID
		if runID == "" {
			runID = a.runInput.RunID
		}
		iter := entry.Iteration
		if iter <= 0 {
			iter = 1
		}

		sb.WriteString(fmt.Sprintf("## %s — %d %s — %s/%s\n", entry.Timestamp, entry.StepIndex, strings.ToUpper(entry.Role), entry.Status, stopReason))
		sb.WriteString(fmt.Sprintf("**Task:** %s  \n", a.runInput.TaskID))
		sb.WriteString(fmt.Sprintf("**Run:** %s · **Iteration:** %d\n\n", runID, iter))
		sb.WriteString(fmt.Sprintf("**Title:** %s\n\n", title))
		sb.WriteString("**Details:**\n")
		if len(entry.Details) == 0 {
			sb.WriteString("- (none)\n")
		} else {
			for _, detail := range entry.Details {
				sb.WriteString(fmt.Sprintf("- %s\n", detail))
			}
		}
		sb.WriteString("\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
