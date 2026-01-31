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

	planAgent  agent.Agent
	doAgent    agent.Agent
	checkAgent agent.Agent
	actAgent   agent.Agent
}

// NewNormaPDCAAgent creates and configures the entire custom agent workflow.
func NewNormaPDCAAgent(cfg config.Config, store *db.Store, tracker task.Tracker, runInput workflows.RunInput, stepIndex *int, baseBranch string) (agent.Agent, error) {
	orchestrator := &NormaPDCAAgent{
		cfg:        cfg,
		store:      store,
		tracker:    tracker,
		runInput:   runInput,
		stepIndex:  stepIndex,
		baseBranch: baseBranch,
	}

	orchestrator.planAgent = orchestrator.createSubAgent(normaloop.RolePlan)
	orchestrator.doAgent = orchestrator.createSubAgent(normaloop.RoleDo)
	orchestrator.checkAgent = orchestrator.createSubAgent(normaloop.RoleCheck)
	orchestrator.actAgent = orchestrator.createSubAgent(normaloop.RoleAct)

	return agent.New(agent.Config{
		Name:        "NormaPDCAAgent",
		Description: "Orchestrates story generation, critique, revision, and checks.",
		SubAgents:   []agent.Agent{orchestrator.planAgent, orchestrator.doAgent, orchestrator.checkAgent, orchestrator.actAgent},
		Run:         orchestrator.Run,
	})
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

	log.Debug().Str("role", roleName).Interface("cmd", cmd).Msg("ADK PDCA Agent: creating ExecAgent")

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