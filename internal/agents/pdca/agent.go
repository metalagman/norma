package pdca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/normahq/norma/internal/agents/pdca/contracts"
	"github.com/normahq/norma/internal/agents/pdca/roles/act"
	"github.com/normahq/norma/internal/agents/pdca/roles/check"
	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/internal/db"
	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/internal/logging"
	"github.com/normahq/norma/internal/task"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/session"
)

// runtime holds PDCA step execution state used by role subagents.
type runtime struct {
	cfg           config.Config
	maxIterations int
	store         *db.Store
	tracker       task.Tracker
	runInput      AgentInput
	baseBranch    string
	embeddedMCP   *embeddedMCPServers
}

// NewLoopAgent creates and configures the PDCA loop agent with role subagents.
func NewLoopAgent(ctx context.Context, cfg config.Config, store *db.Store, tracker task.Tracker, runInput AgentInput, baseBranch string, maxIterations int) (agent.Agent, error) {
	// Start embedded MCP servers for inter-process state sharing
	embeddedMCP, mcpServers, err := startEmbeddedMCPServers(ctx, runInput.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("start embedded MCP servers: %w", err)
	}

	rt := &runtime{
		cfg:           cfg,
		maxIterations: maxIterations,
		store:         store,
		tracker:       tracker,
		runInput:      runInput,
		baseBranch:    baseBranch,
		embeddedMCP:   embeddedMCP,
	}

	planAgent, err := rt.createSubAgent(ctx, RolePlan, mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RolePlan, err)
	}
	doAgent, err := rt.createSubAgent(ctx, RoleDo, mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RoleDo, err)
	}
	checkAgent, err := rt.createSubAgent(ctx, RoleCheck, mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RoleCheck, err)
	}
	actAgent, err := rt.createSubAgent(ctx, RoleAct, mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RoleAct, err)
	}

	ag, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(maxIterations),
		AgentConfig: agent.Config{
			Name:        "PDCALoop",
			Description: "ADK loop agent for PDCA",
			SubAgents:   []agent.Agent{planAgent, doAgent, checkAgent, actAgent},
		},
	})
	if err != nil {
		_ = embeddedMCP.close()
		return nil, err
	}
	return ag, nil
}

func (a *runtime) createSubAgent(ctx context.Context, roleName string, mcpServers map[string]agentconfig.MCPServerConfig) (agent.Agent, error) {
	pascalName := ""
	switch roleName {
	case RolePlan:
		pascalName = "Plan"
	case RoleDo:
		pascalName = "Do"
	case RoleCheck:
		pascalName = "Check"
	case RoleAct:
		pascalName = "Act"
	default:
		// Simple manual title case to avoid deprecated strings.Title
		if len(roleName) > 0 {
			pascalName = strings.ToUpper(roleName[:1]) + roleName[1:]
		}
	}
	ag, err := agent.New(agent.Config{
		Name:        pascalName,
		Description: fmt.Sprintf("Norma %s agent", pascalName),
		Run:         a.runRoleLoop(ctx, roleName, mcpServers),
	})
	if err != nil {
		return nil, err
	}
	return ag, nil
}

func (a *runtime) runRoleLoop(ctx context.Context, roleName string, mcpServers map[string]agentconfig.MCPServerConfig) func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		l := log.With().
			Str("component", "pdca").
			Str("agent_name", ctx.Agent().Name()).
			Str("invocation_id", ctx.InvocationID()).
			Logger()

		return func(yield func(*session.Event, error) bool) {
			if ctx.Ended() || a.shouldStop(ctx) {
				return
			}

			iteration, err := ctx.Session().State().Get("iteration")
			itNum, ok := iteration.(int)
			if err != nil || !ok {
				itNum = 1
			}

			l.Info().Int("iteration", itNum).Msg("starting step")
			resp, err := a.runStep(ctx, itNum, roleName, mcpServers)
			if err != nil {
				l.Error().Err(err).Msg("step failed")
				yield(nil, err)
				return
			}
			if err := validateStepResponse(roleName, resp); err != nil {
				l.Error().Err(err).Msg("invalid step response")
				yield(nil, err)
				return
			}

			l.Debug().Str("status", resp.Status).Msg("step completed")

			a.processRoleResult(ctx, yield, roleName, resp, itNum)
		}
	}
}

func (a *runtime) processRoleResult(ctx agent.InvocationContext, yield func(*session.Event, error) bool, roleName string, resp *contracts.RawAgentResponse, itNum int) {
	l := log.With().
		Str("component", "pdca").
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
		Logger()

	// Communicate results via session state
	if roleName == RoleCheck && resp.CheckOutput != nil {
		var checkOut check.CheckOutput
		if err := json.Unmarshal(resp.CheckOutput, &checkOut); err == nil {
			l.Debug().Str("verdict", checkOut.Verdict.Status).Msg("setting check verdict in state")
			if err := ctx.Session().State().Set("verdict", checkOut.Verdict.Status); err != nil {
				yield(nil, fmt.Errorf("set verdict in session state: %w", err))
				return
			}
		}
	}
	if roleName == RoleAct && resp.ActOutput != nil {
		var actOut act.ActOutput
		if err := json.Unmarshal(resp.ActOutput, &actOut); err == nil {
			l.Debug().Str("decision", actOut.Decision).Msg("setting act decision in state")
			if err := ctx.Session().State().Set("decision", actOut.Decision); err != nil {
				yield(nil, fmt.Errorf("set decision in session state: %w", err))
				return
			}
			if actOut.Decision == "close" {
				l.Info().Msg("act decision is close, stopping loop")
				if err := ctx.Session().State().Set("stop", true); err != nil {
					yield(nil, fmt.Errorf("set stop flag in session state: %w", err))
					return
				}
				ev := session.NewEvent(ctx.InvocationID())
				ev.Actions.Escalate = true
				_ = yield(ev, nil)
				return
			}
			if err := ctx.Session().State().Set("iteration", itNum+1); err != nil {
				yield(nil, fmt.Errorf("update iteration in session state: %w", err))
				return
			}
		}
	}
	if resp.Status != "ok" {
		l.Warn().Str("role", roleName).Str("status", resp.Status).Msg("non-ok status, stopping loop")
		if err := ctx.Session().State().Set("stop", true); err != nil {
			yield(nil, fmt.Errorf("set stop flag in session state: %w", err))
			return
		}
		ev := session.NewEvent(ctx.InvocationID())
		ev.Actions.Escalate = true
		_ = yield(ev, nil)
		return
	}
}

func (a *runtime) shouldStop(ctx agent.InvocationContext) bool {
	stop, err := ctx.Session().State().Get("stop")
	if err != nil {
		return false
	}
	if b, ok := stop.(bool); ok {
		return b
	}
	if s, ok := stop.(string); ok {
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(s))
		return parseErr == nil && parsed
	}
	return false
}

func (a *runtime) runStep(ctx agent.InvocationContext, iteration int, roleName string, mcpServers map[string]agentconfig.MCPServerConfig) (*contracts.RawAgentResponse, error) {
	if a.tracker != nil {
		workflowState := ""
		switch roleName {
		case RolePlan:
			workflowState = "planning"
		case RoleDo:
			workflowState = "doing"
		case RoleCheck:
			workflowState = "checking"
		case RoleAct:
			workflowState = "acting"
		}

		if workflowState != "" {
			if err := a.tracker.UpdateWorkflowState(ctx, a.runInput.TaskID, workflowState); err != nil {
				log.Warn().Err(err).Str("task_id", a.runInput.TaskID).Str("state", workflowState).Msg("failed to update workflow state in tracker")
			}
		}

		// Check for skip labels
		skipLabel := ""
		switch roleName {
		case RolePlan:
			skipLabel = "norma-has-plan"
		case RoleDo:
			skipLabel = "norma-has-do"
		case RoleCheck:
			skipLabel = "norma-has-check"
		}
		if skipLabel != "" {
			item, err := a.tracker.Task(ctx, a.runInput.TaskID)
			if err == nil {
				hasLabel := false
				for _, l := range item.Labels {
					if l == skipLabel {
						hasLabel = true
						break
					}
				}
				if hasLabel {
					log.Info().Str("task_id", a.runInput.TaskID).Str("role", roleName).Msg("skipping step due to label")
					state := a.getTaskState(ctx)
					resp := &contracts.RawAgentResponse{
						Status: "ok",
						Summary: contracts.ResponseSummary{
							Text: fmt.Sprintf("Skipped %s step: already completed (label %s found)", roleName, skipLabel),
						},
						Progress: contracts.StepProgress{
							Title:   fmt.Sprintf("%s skipped (resumed)", roleName),
							Details: []string{fmt.Sprintf("Label %s is present on task", skipLabel)},
						},
					}
					// Restore state from task notes if possible
					if state.Plan != nil {
						if data, err := json.Marshal(state.Plan); err == nil {
							resp.PlanOutput = data
						}
					}
					if state.Do != nil {
						if data, err := json.Marshal(state.Do); err == nil {
							resp.DoOutput = data
						}
					}
					if state.Check != nil {
						if data, err := json.Marshal(state.Check); err == nil {
							resp.CheckOutput = data
						}
					}
					if state.Act != nil {
						if data, err := json.Marshal(state.Act); err == nil {
							resp.ActOutput = data
						}
					}

					// Increment step index
					idxVal, _ := ctx.Session().State().Get("current_step_index")
					index := 0
					if idxVal != nil {
						if i, ok := idxVal.(int); ok {
							index = i
						}
					}
					index++
					_ = ctx.Session().State().Set("current_step_index", index)

					// Commit a "skipped" step record to DB
					if a.store != nil {
						now := time.Now().UTC().Format(time.RFC3339)
						stepRec := db.StepRecord{
							RunID:     a.runInput.RunID,
							StepIndex: index,
							Role:      roleName,
							Iteration: iteration,
							Status:    "skipped",
							StepDir:   "", // No directory for skipped step
							StartedAt: now,
							EndedAt:   now,
							Summary:   resp.Summary.Text,
						}
						update := db.Update{
							CurrentStepIndex: index,
							Iteration:        iteration,
							Status:           "running",
						}
						_ = a.store.CommitStep(ctx, stepRec, nil, update)
					}

					// Update journal
					_ = a.updateTaskState(ctx, resp, roleName, iteration, index)

					return resp, nil
				}
			}
		}
	}

	idxVal, err := ctx.Session().State().Get("current_step_index")
	index := 0
	if err == nil && idxVal != nil {
		if i, ok := idxVal.(int); ok {
			index = i
		}
	}
	index++

	if err := ctx.Session().State().Set("current_step_index", index); err != nil {
		return nil, fmt.Errorf("set current_step_index in session state: %w", err)
	}

	role := Role(roleName)
	if role == nil {
		return nil, fmt.Errorf("unknown role %q", roleName)
	}

	req := a.baseRequest(iteration, index, roleName)

	// Pass TaskState to all roles - each role reads what it needs
	state := a.getTaskState(ctx)
	req.TaskState = *state

	// Prepare step directory and workspace
	stepsDir := filepath.Join(a.runInput.RunDir, "steps")
	stepDirName := fmt.Sprintf("%03d-%s", index, roleName)
	stepDir := filepath.Join(stepsDir, stepDirName)
	if err := os.MkdirAll(filepath.Join(stepDir, "logs"), 0o700); err != nil {
		return nil, err
	}

	l := log.With().
		Str("component", "pdca").
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
		Logger()

	workspaceDir := filepath.Join(stepDir, "workspace")
	branchName := fmt.Sprintf("norma/task/%s", a.runInput.TaskID)
	l.Debug().Str("workspace", workspaceDir).Str("branch", branchName).Msg("mounting worktree")
	if _, err := git.MountWorktree(ctx, a.runInput.WorkingDir, workspaceDir, branchName, a.baseBranch); err != nil {
		return nil, fmt.Errorf("mount worktree: %w", err)
	}
	defer func() {
		l.Debug().Str("workspace", workspaceDir).Msg("removing worktree")
		if err := git.RemoveWorktree(ctx, a.runInput.WorkingDir, workspaceDir); err != nil {
			l.Warn().Err(err).Str("workspace", workspaceDir).Msg("failed to remove worktree")
		}
	}()

	absStepDir, err := filepath.Abs(stepDir)
	if err != nil {
		return nil, fmt.Errorf("resolve step dir path: %w", err)
	}
	absWorkspaceDir, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace dir path: %w", err)
	}

	req.Paths = contracts.RequestPaths{
		WorkspaceDir: absWorkspaceDir,
		RunDir:       absStepDir,
	}

	// Create input.json
	inputData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal input.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "input.json"), inputData, 0o600); err != nil {
		return nil, fmt.Errorf("write input.json: %w", err)
	}

	// Create runner for this step
	agentCfg, err := resolvedAgentForRole(a.cfg.Norma.Providers, a.cfg.RoleIDs, roleName)
	if err != nil {
		return nil, err
	}
	runner, err := NewRunner(agentCfg, role, mcpServers)
	if err != nil {
		return nil, fmt.Errorf("create runner for role %q: %w", roleName, err)
	}
	l.Debug().Str("role", roleName).Str("agent_type", agentCfg.Type).Msg("running step runner")

	// Prepare log files
	stdoutFile, err := os.OpenFile(filepath.Join(stepDir, "logs", "stdout.txt"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create stdout log file: %w", err)
	}
	defer func() { _ = stdoutFile.Close() }()

	stderrFile, err := os.OpenFile(filepath.Join(stepDir, "logs", "stderr.txt"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create stderr log file: %w", err)
	}
	defer func() { _ = stderrFile.Close() }()

	eventsFile, err := os.OpenFile(filepath.Join(stepDir, "logs", "events.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create events log file: %w", err)
	}
	defer func() { _ = eventsFile.Close() }()

	multiStdout, multiStderr := agentOutputWriters(logging.DebugEnabled(), stdoutFile, stderrFile)

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	startTime := time.Now()
	lastOut, _, exitCode, err := runner.Run(ctx, reqBytes, multiStdout, multiStderr, eventsFile)
	if err != nil {
		return nil, fmt.Errorf("run role %q agent (exit code %d): %w", roleName, exitCode, err)
	}
	endTime := time.Now()

	// Parse response
	resp, err := role.MapResponse(lastOut)
	if err != nil {
		return nil, fmt.Errorf("map response: %w", err)
	}

	// Persist output.json
	respJSON, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal output.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "output.json"), respJSON, 0o600); err != nil {
		return nil, fmt.Errorf("write output.json: %w", err)
	}

	// Persist Do workspace changes before worktree cleanup.
	if roleName == RoleDo && resp.Status == "ok" {
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
	if err := a.store.CommitStep(ctx, stepRec, nil, update); err != nil {
		return nil, fmt.Errorf("commit step %d (%s): %w", index, roleName, err)
	}

	// Update Task State and persist to Beads.
	if err := a.updateTaskState(ctx, &resp, roleName, iteration, index); err != nil {
		return nil, err
	}

	if a.tracker != nil && resp.Status == "ok" {
		label := ""
		switch roleName {
		case RolePlan:
			label = "norma-has-plan"
		case RoleDo:
			label = "norma-has-do"
		case RoleCheck:
			label = "norma-has-check"
		}
		if label != "" {
			if err := a.tracker.AddLabel(ctx, a.runInput.TaskID, label); err != nil {
				log.Warn().Err(err).Str("task_id", a.runInput.TaskID).Str("label", label).Msg("failed to add label to task")
			}
		}
	}

	return &resp, nil
}

func agentOutputWriters(debugEnabled bool, stdoutLog io.Writer, stderrLog io.Writer) (io.Writer, io.Writer) {
	if !debugEnabled {
		return stdoutLog, stderrLog
	}
	return io.MultiWriter(os.Stdout, stdoutLog), io.MultiWriter(os.Stderr, stderrLog)
}

func (a *runtime) baseRequest(iteration, index int, role string) contracts.AgentRequest {
	return contracts.AgentRequest{
		Run: contracts.RunInfo{
			ID:        a.runInput.RunID,
			Iteration: iteration,
		},
		Task: contracts.TaskInfo{
			ID:                 a.runInput.TaskID,
			Title:              a.runInput.Goal,
			Description:        a.runInput.Goal,
			AcceptanceCriteria: a.runInput.AcceptanceCriteria,
		},
		Step: contracts.StepInfo{
			Index: index,
			Name:  role,
		},
		Budgets: contracts.Budgets{
			MaxIterations: a.maxIterations,
		},
		StopReasonsAllowed: []string{
			"budget_exceeded",
			"dependency_blocked",
			"verify_missing",
			"replan_required",
		},
	}
}

func validateStepResponse(roleName string, resp *contracts.RawAgentResponse) error {
	if resp == nil {
		return fmt.Errorf("nil response for role %q", roleName)
	}

	switch resp.Status {
	case "ok", "stop", "error":
	default:
		return fmt.Errorf("%s step returned non-ok status %q", roleName, resp.Status)
	}
	if resp.Status == "stop" || resp.Status == "error" {
		return nil
	}

	switch roleName {
	case RolePlan:
		if resp.PlanOutput == nil {
			return fmt.Errorf("plan step returned status ok without plan output")
		}
	case RoleDo:
		if resp.DoOutput == nil {
			return fmt.Errorf("do step returned status ok without do output")
		}
	case RoleCheck:
		if resp.CheckOutput == nil {
			return fmt.Errorf("check step returned status ok without check output")
		}
	case RoleAct:
		if resp.ActOutput == nil {
			return fmt.Errorf("act step returned status ok without act output")
		}
	default:
		return fmt.Errorf("unknown role %q", roleName)
	}

	return nil
}

func resolvedAgentForRole(registry map[string]config.AgentConfig, roleIDs map[string]string, roleName string) (config.AgentConfig, error) {
	agentID, ok := roleIDs[roleName]
	if !ok {
		return config.AgentConfig{}, fmt.Errorf("missing agent reference for role %q in profile", roleName)
	}
	agentCfg, ok := registry[agentID]
	if !ok {
		return config.AgentConfig{}, fmt.Errorf("missing resolved agent config for agent %q (role %q)", agentID, roleName)
	}
	return agentCfg, nil
}

func (a *runtime) getTaskState(ctx agent.InvocationContext) *contracts.TaskState {
	s, err := ctx.Session().State().Get("task_state")
	if err != nil {
		return &contracts.TaskState{}
	}
	return coerceTaskState(s)
}

func coerceTaskState(value any) *contracts.TaskState {
	switch state := value.(type) {
	case nil:
		return &contracts.TaskState{}
	case *contracts.TaskState:
		if state == nil {
			return &contracts.TaskState{}
		}
		return state
	case contracts.TaskState:
		copied := state
		return &copied
	default:
		// Handle map case by marshaling to JSON and back
		if m, ok := value.(map[string]any); ok {
			var result contracts.TaskState
			// Marshal the whole map to JSON then unmarshal into TaskState
			data, err := json.Marshal(m)
			if err == nil {
				if err := json.Unmarshal(data, &result); err == nil {
					return &result
				}
			}
		}
		return &contracts.TaskState{}
	}
}

func (a *runtime) updateTaskState(ctx agent.InvocationContext, resp *contracts.RawAgentResponse, role string, iteration, index int) error {
	if resp == nil {
		return fmt.Errorf("nil agent response for role %q", role)
	}

	state := a.getTaskState(ctx)
	applyAgentResponseToTaskState(state, resp, role, a.runInput.RunID, iteration, index, time.Now())

	if err := ctx.Session().State().Set("task_state", state); err != nil {
		return fmt.Errorf("set task state in session: %w", err)
	}

	if a.tracker != nil {
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task state: %w", err)
		}
		if err := a.tracker.SetNotes(ctx, a.runInput.TaskID, string(data)); err != nil {
			return fmt.Errorf("persist task state to beads: %w", err)
		}
	}

	return nil
}

func applyAgentResponseToTaskState(state *contracts.TaskState, resp *contracts.RawAgentResponse, role, runID string, iteration, index int, now time.Time) {
	switch role {
	case RolePlan:
		if resp.PlanOutput != nil {
			if err := json.Unmarshal(resp.PlanOutput, &state.Plan); err != nil {
				log.Warn().Err(err).Msg("unmarshal plan output to task state")
			}
		}
	case RoleDo:
		if resp.DoOutput != nil {
			if err := json.Unmarshal(resp.DoOutput, &state.Do); err != nil {
				log.Warn().Err(err).Msg("unmarshal do output to task state")
			}
		}
	case RoleCheck:
		if resp.CheckOutput != nil {
			if err := json.Unmarshal(resp.CheckOutput, &state.Check); err != nil {
				log.Warn().Err(err).Msg("unmarshal check output to task state")
			}
		}
	case RoleAct:
		if resp.ActOutput != nil {
			if err := json.Unmarshal(resp.ActOutput, &state.Act); err != nil {
				log.Warn().Err(err).Msg("unmarshal act output to task state")
			}
		}
	}

	entry := contracts.JournalEntry{
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
	statusOut, err := git.GitRunCmdOutput(ctx, workspaceDir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read workspace status: %w", err)
	}
	status := strings.TrimSpace(statusOut)
	if status == "" {
		return nil
	}

	if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("stage workspace changes: %w", err)
	}

	commitMsg := fmt.Sprintf("chore: do step %03d\n\nRun: %s\nTask: %s", stepIndex, runID, taskID)
	if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("commit workspace changes: %w", err)
	}

	return nil
}
