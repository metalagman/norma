package pdca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/normahq/norma/v2/internal/agents/pdca/contracts"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/act"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/check"
	"github.com/normahq/norma/v2/internal/config"
	"github.com/normahq/norma/v2/internal/db"
	"github.com/normahq/norma/v2/internal/git"
	"github.com/normahq/norma/v2/internal/logging"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/normahq/runtime/v2/agentconfig"
	"github.com/normahq/runtime/v2/structuredagent"
	"github.com/rs/zerolog/log"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/workflowagents/loopagent"
	"google.golang.org/adk/v2/session"
)

const stopReasonReplanRequired = "replan_required"

type runtime struct {
	cfg           config.Config
	maxIterations int
	store         *db.Store
	tracker       task.Tracker
	runInput      AgentInput
	baseBranch    string
	embeddedMCP   *embeddedMCPServers
	workspaceOnce sync.Once
	workspaceDir  string
	workspaceErr  error
}

type roleAgent struct {
	runtime     *runtime
	role        contracts.Role
	roleName    string
	displayName string
	workflow    string
	runner      Runner
	commitOnOK  bool
	hasOutput   func(*contracts.RawAgentResponse) bool
	persist     func(*contracts.TaskState, *contracts.RawAgentResponse)
	afterOK     func(agent.InvocationContext, func(*session.Event, error) bool, *contracts.RawAgentResponse, int) bool
}

type roleAgentSpec struct {
	roleName    string
	displayName string
	workflow    string
	commitOnOK  bool
	hasOutput   func(*contracts.RawAgentResponse) bool
	persist     func(*contracts.TaskState, *contracts.RawAgentResponse)
	afterOK     func(agent.InvocationContext, func(*session.Event, error) bool, *contracts.RawAgentResponse, int) bool
}

func planRoleAgentSpec() roleAgentSpec {
	return roleAgentSpec{
		roleName:    RolePlan,
		displayName: "Plan",
		workflow:    "planning",
		hasOutput: func(resp *contracts.RawAgentResponse) bool {
			return resp.PlanOutput != nil
		},
		persist: func(state *contracts.TaskState, resp *contracts.RawAgentResponse) {
			persistRoleOutput("plan", resp.PlanOutput, &state.Plan)
		},
	}
}

func doRoleAgentSpec() roleAgentSpec {
	return roleAgentSpec{
		roleName:    RoleDo,
		displayName: "Do",
		workflow:    "doing",
		commitOnOK:  true,
		hasOutput: func(resp *contracts.RawAgentResponse) bool {
			return resp.DoOutput != nil
		},
		persist: func(state *contracts.TaskState, resp *contracts.RawAgentResponse) {
			persistRoleOutput("do", resp.DoOutput, &state.Do)
		},
	}
}

func checkRoleAgentSpec() roleAgentSpec {
	return roleAgentSpec{
		roleName:    RoleCheck,
		displayName: "Check",
		workflow:    "checking",
		hasOutput: func(resp *contracts.RawAgentResponse) bool {
			return resp.CheckOutput != nil
		},
		persist: func(state *contracts.TaskState, resp *contracts.RawAgentResponse) {
			persistRoleOutput("check", resp.CheckOutput, &state.Check)
		},
		afterOK: setCheckVerdict,
	}
}

func actRoleAgentSpec() roleAgentSpec {
	return roleAgentSpec{
		roleName:    RoleAct,
		displayName: "Act",
		workflow:    "acting",
		hasOutput: func(resp *contracts.RawAgentResponse) bool {
			return resp.ActOutput != nil
		},
		persist: func(state *contracts.TaskState, resp *contracts.RawAgentResponse) {
			persistRoleOutput("act", resp.ActOutput, &state.Act)
		},
		afterOK: setActDecision,
	}
}

func roleAgentSpecByName(roleName string) (roleAgentSpec, bool) {
	for _, spec := range []roleAgentSpec{
		planRoleAgentSpec(),
		doRoleAgentSpec(),
		checkRoleAgentSpec(),
		actRoleAgentSpec(),
	} {
		if spec.roleName == roleName {
			return spec, true
		}
	}
	return roleAgentSpec{}, false
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

	planAgent, err := rt.newRoleAgent(ctx, planRoleAgentSpec(), mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RolePlan, err)
	}
	doAgent, err := rt.newRoleAgent(ctx, doRoleAgentSpec(), mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RoleDo, err)
	}
	checkAgent, err := rt.newRoleAgent(ctx, checkRoleAgentSpec(), mcpServers)
	if err != nil {
		_ = embeddedMCP.close()
		return nil, fmt.Errorf("create %s subagent: %w", RoleCheck, err)
	}
	actAgent, err := rt.newRoleAgent(ctx, actRoleAgentSpec(), mcpServers)
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

func (a *runtime) newRoleAgent(ctx context.Context, spec roleAgentSpec, mcpServers map[string]agentconfig.MCPServerConfig) (agent.Agent, error) {
	role := Role(spec.roleName)
	if role == nil {
		return nil, fmt.Errorf("unknown role %q", spec.roleName)
	}
	agentCfg, err := resolvedAgentForRole(a.cfg.Runtime.Providers, a.cfg.RoleIDs, spec.roleName)
	if err != nil {
		return nil, err
	}
	runner, err := NewRunner(agentCfg, role, mcpServers)
	if err != nil {
		return nil, fmt.Errorf("create runner for role %q: %w", spec.roleName, err)
	}
	roleRuntime := &roleAgent{
		runtime:     a,
		role:        role,
		roleName:    spec.roleName,
		displayName: spec.displayName,
		workflow:    spec.workflow,
		runner:      runner,
		commitOnOK:  spec.commitOnOK,
		hasOutput:   spec.hasOutput,
		persist:     spec.persist,
		afterOK:     spec.afterOK,
	}
	ag, err := agent.New(agent.Config{
		Name:        spec.displayName,
		Description: fmt.Sprintf("Norma %s agent", spec.displayName),
		Run:         roleRuntime.run(ctx),
	})
	if err != nil {
		return nil, err
	}
	return ag, nil
}

func (r *roleAgent) run(ctx context.Context) func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		l := log.With().
			Str("component", "pdca").
			Str("agent_name", ctx.Agent().Name()).
			Str("invocation_id", ctx.InvocationID()).
			Logger()

		return func(yield func(*session.Event, error) bool) {
			if ctx.Ended() || r.runtime.shouldStop(ctx) {
				return
			}

			iteration, err := ctx.Session().State().Get("iteration")
			itNum, ok := iteration.(int)
			if err != nil || !ok {
				itNum = 1
			}

			l.Info().Int("iteration", itNum).Msg("starting step")
			resp, err := r.runStep(ctx, itNum)
			if err != nil {
				l.Error().Err(err).Msg("step failed")
				yield(nil, err)
				return
			}
			if err := r.validateResponse(resp); err != nil {
				l.Error().Err(err).Msg("invalid step response")
				yield(nil, err)
				return
			}

			l.Debug().Str("status", resp.Status).Msg("step completed")

			r.processResult(ctx, yield, resp, itNum)
		}
	}
}

func (r *roleAgent) processResult(ctx agent.InvocationContext, yield func(*session.Event, error) bool, resp *contracts.RawAgentResponse, itNum int) {
	l := log.With().
		Str("component", "pdca").
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
		Logger()

	if resp.Status == "ok" && r.afterOK != nil {
		if keepGoing := r.afterOK(ctx, yield, resp, itNum); !keepGoing {
			return
		}
	}
	if resp.Status != "ok" {
		l.Warn().Str("role", r.roleName).Str("status", resp.Status).Msg("non-ok status, stopping loop")
		if err := ctx.Session().State().Set("stop", true); err != nil {
			yield(nil, fmt.Errorf("set stop flag in session state: %w", err))
			return
		}
		ev := session.NewEvent(context.Background(), ctx.InvocationID())
		ev.Actions.Escalate = true
		_ = yield(ev, nil)
		return
	}
}

func setCheckVerdict(ctx agent.InvocationContext, yield func(*session.Event, error) bool, resp *contracts.RawAgentResponse, _ int) bool {
	if resp.CheckOutput == nil {
		return true
	}
	var checkOut check.CheckOutput
	if err := json.Unmarshal(resp.CheckOutput, &checkOut); err != nil {
		log.Warn().Err(err).Msg("unmarshal check output for verdict")
		return true
	}
	log.Debug().Str("verdict", checkOut.Verdict).Msg("setting check verdict in state")
	if err := ctx.Session().State().Set("verdict", checkOut.Verdict); err != nil {
		yield(nil, fmt.Errorf("set verdict in session state: %w", err))
		return false
	}
	return true
}

func setActDecision(ctx agent.InvocationContext, yield func(*session.Event, error) bool, resp *contracts.RawAgentResponse, itNum int) bool {
	if resp.ActOutput == nil {
		return true
	}
	var actOut act.ActOutput
	if err := json.Unmarshal(resp.ActOutput, &actOut); err != nil {
		log.Warn().Err(err).Msg("unmarshal act output for decision")
		return true
	}
	log.Debug().Str("decision", actOut.Decision).Msg("setting act decision in state")
	if err := ctx.Session().State().Set("decision", actOut.Decision); err != nil {
		yield(nil, fmt.Errorf("set decision in session state: %w", err))
		return false
	}
	if actOut.Decision == actDecisionClose {
		log.Info().Msg("act decision is close, stopping loop")
		if err := ctx.Session().State().Set("stop", true); err != nil {
			yield(nil, fmt.Errorf("set stop flag in session state: %w", err))
			return false
		}
		ev := session.NewEvent(context.Background(), ctx.InvocationID())
		ev.Actions.Escalate = true
		_ = yield(ev, nil)
		return false
	}
	if err := ctx.Session().State().Set("iteration", itNum+1); err != nil {
		yield(nil, fmt.Errorf("update iteration in session state: %w", err))
		return false
	}
	return true
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

func (r *roleAgent) runStep(ctx agent.InvocationContext, iteration int) (*contracts.RawAgentResponse, error) {
	if r.runtime.tracker != nil && r.workflow != "" {
		if err := r.runtime.tracker.UpdateWorkflowState(ctx, r.runtime.runInput.TaskID, r.workflow); err != nil {
			log.Warn().Err(err).Str("task_id", r.runtime.runInput.TaskID).Str("state", r.workflow).Msg("failed to update workflow state in tracker")
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

	req := r.runtime.baseRequest(iteration, index, r.roleName)

	// Pass TaskState to all roles - each role reads what it needs
	state := r.runtime.getTaskState(ctx)
	req.TaskState = *state

	// Prepare step directory. The git workspace is shared for the whole run.
	stepsDir := filepath.Join(r.runtime.runInput.RunDir, "steps")
	stepDirName := fmt.Sprintf("%03d-%s", index, r.roleName)
	stepDir := filepath.Join(stepsDir, stepDirName)
	if err := os.MkdirAll(filepath.Join(stepDir, "logs"), 0o700); err != nil {
		return nil, err
	}

	l := log.With().
		Str("component", "pdca").
		Str("agent_name", ctx.Agent().Name()).
		Str("invocation_id", ctx.InvocationID()).
		Logger()

	workspaceDir, err := r.runtime.sharedWorkspace(ctx)
	if err != nil {
		return nil, err
	}

	req.Paths = contracts.RequestPaths{
		WorkspaceDir: workspaceDir,
	}

	// Create input.json
	inputData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal input.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "input.json"), inputData, 0o600); err != nil {
		return nil, fmt.Errorf("write input.json: %w", err)
	}

	l.Debug().Str("role", r.roleName).Msg("running step runner")

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
	resp, exitCode, err := r.runner.Run(ctx, reqBytes, multiStdout, multiStderr, eventsFile)
	if err != nil {
		return r.persistStepFailure(ctx, iteration, index, stepDir, startTime, fmt.Errorf("run role %q agent (exit code %d): %w", r.roleName, exitCode, err), exitCode)
	}
	endTime := time.Now()

	// Persist output.json
	respJSON, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal output.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "output.json"), respJSON, 0o600); err != nil {
		return nil, fmt.Errorf("write output.json: %w", err)
	}

	if !r.commitOnOK {
		if err := ensureWorkspaceClean(ctx, workspaceDir, r.roleName); err != nil {
			return r.persistStepFailure(ctx, iteration, index, stepDir, startTime, err, 0)
		}
	}

	if r.commitOnOK && resp.Status == "ok" {
		if err := commitWorkspaceChanges(ctx, workspaceDir, r.runtime.runInput.RunID, r.runtime.runInput.TaskID, index); err != nil {
			return r.persistStepFailure(ctx, iteration, index, stepDir, startTime, err, 0)
		}
	}

	// Commit to DB
	stepRec := db.StepRecord{
		RunID:     r.runtime.runInput.RunID,
		StepIndex: index,
		Role:      r.roleName,
		Iteration: iteration,
		Status:    resp.Status,
		StepDir:   stepDir,
		StartedAt: startTime.UTC().Format(time.RFC3339),
		EndedAt:   endTime.UTC().Format(time.RFC3339),
		Summary:   resp.Summary,
	}
	update := db.Update{
		CurrentStepIndex: index,
		Iteration:        iteration,
		Status:           "running",
	}
	if err := r.runtime.store.CommitStep(ctx, stepRec, nil, update); err != nil {
		return nil, fmt.Errorf("commit step %d (%s): %w", index, r.roleName, err)
	}

	// Update ephemeral task state for the remaining roles in this run.
	if err := r.updateTaskState(ctx, &resp, iteration, index); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (r *roleAgent) persistStepFailure(
	ctx agent.InvocationContext,
	iteration int,
	index int,
	stepDir string,
	startTime time.Time,
	runErr error,
	exitCode int,
) (*contracts.RawAgentResponse, error) {
	resp := stepFailureResponse(r.roleName, runErr, exitCode)

	respJSON, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal fallback output.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stepDir, "output.json"), respJSON, 0o600); err != nil {
		return nil, fmt.Errorf("write fallback output.json: %w", err)
	}

	if r.runtime.store != nil {
		endTime := time.Now()
		stepRec := db.StepRecord{
			RunID:     r.runtime.runInput.RunID,
			StepIndex: index,
			Role:      r.roleName,
			Iteration: iteration,
			Status:    "fail",
			StepDir:   stepDir,
			StartedAt: startTime.UTC().Format(time.RFC3339),
			EndedAt:   endTime.UTC().Format(time.RFC3339),
			Summary:   resp.Summary,
		}
		update := db.Update{
			CurrentStepIndex: index,
			Iteration:        iteration,
			Status:           "running",
		}
		if err := r.runtime.store.CommitStep(ctx, stepRec, nil, update); err != nil {
			return nil, fmt.Errorf("commit failed step %d (%s): %w", index, r.roleName, err)
		}
	}

	if err := r.updateTaskState(ctx, resp, iteration, index); err != nil {
		return nil, err
	}

	return resp, nil
}

func stepFailureResponse(roleName string, runErr error, exitCode int) *contracts.RawAgentResponse {
	stopReason := stepFailureStopReason(runErr)
	summary := fmt.Sprintf("%s step failed: %s", roleName, compactErrorText(runErr, 240))
	if exitCode > 0 {
		summary = fmt.Sprintf("%s (exit_code=%d)", summary, exitCode)
	}
	return &contracts.RawAgentResponse{
		Status:     "error",
		StopReason: stopReason,
		Summary:    summary,
	}
}

func stepFailureStopReason(runErr error) string {
	if runErr == nil {
		return stopReasonReplanRequired
	}

	if errors.Is(runErr, context.DeadlineExceeded) {
		return "budget_exceeded"
	}
	if errors.Is(runErr, structuredagent.ErrStructuredIOSchemaValidation) ||
		errors.Is(runErr, structuredagent.ErrStructuredInputSchemaValidation) ||
		errors.Is(runErr, structuredagent.ErrStructuredOutputSchemaValidation) {
		return stopReasonReplanRequired
	}

	lower := strings.ToLower(runErr.Error())
	if strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") {
		return "budget_exceeded"
	}
	if strings.Contains(lower, "dependency") && strings.Contains(lower, "block") {
		return "dependency_blocked"
	}

	return stopReasonReplanRequired
}

func compactErrorText(err error, maxLen int) string {
	if err == nil {
		return "unknown error"
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "unknown error"
	}
	text = strings.ReplaceAll(text, "\n", " | ")
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

func agentOutputWriters(debugEnabled bool, stdoutLog io.Writer, stderrLog io.Writer) (io.Writer, io.Writer) {
	if !debugEnabled {
		return stdoutLog, stderrLog
	}
	return io.MultiWriter(os.Stdout, stdoutLog), io.MultiWriter(os.Stderr, stderrLog)
}

func (a *runtime) sharedWorkspace(ctx context.Context) (string, error) {
	a.workspaceOnce.Do(func() {
		workspaceDir := filepath.Join(a.runInput.RunDir, "workspace")
		branchName := fmt.Sprintf("norma/task/%s", a.runInput.TaskID)
		log.Debug().Str("workspace", workspaceDir).Str("branch", branchName).Msg("mounting shared PDCA worktree")
		if _, err := git.MountWorktree(ctx, a.runInput.WorkingDir, workspaceDir, branchName, a.baseBranch); err != nil {
			a.workspaceErr = fmt.Errorf("mount shared worktree: %w", err)
			return
		}
		absWorkspaceDir, err := filepath.Abs(workspaceDir)
		if err != nil {
			a.workspaceErr = fmt.Errorf("resolve shared workspace dir path: %w", err)
			return
		}
		a.workspaceDir = absWorkspaceDir
	})
	if a.workspaceErr != nil {
		return "", a.workspaceErr
	}
	return a.workspaceDir, nil
}

func (a *runtime) baseRequest(iteration, index int, role string) contracts.AgentRequest {
	return contracts.AgentRequest{
		Run: contracts.RunInfo{
			ID:        a.runInput.RunID,
			Iteration: iteration,
		},
		Task: contracts.TaskInfo{
			ID:                 a.runInput.TaskID,
			Goal:               a.runInput.Goal,
			AcceptanceCriteria: a.runInput.AcceptanceCriteria,
		},
		Step: contracts.StepInfo{
			Index: index,
		},
	}
}

func validateStepResponse(roleName string, resp *contracts.RawAgentResponse) error {
	spec, ok := roleAgentSpecByName(roleName)
	if !ok {
		return fmt.Errorf("unknown role %q", roleName)
	}
	return (&roleAgent{roleName: spec.roleName, hasOutput: spec.hasOutput}).validateResponse(resp)
}

func (r *roleAgent) validateResponse(resp *contracts.RawAgentResponse) error {
	if resp == nil {
		return fmt.Errorf("nil response for role %q", r.roleName)
	}

	switch resp.Status {
	case "ok", "stop", "error":
	default:
		return fmt.Errorf("%s step returned non-ok status %q", r.roleName, resp.Status)
	}
	if resp.Status == "stop" || resp.Status == "error" {
		return nil
	}
	if r.hasOutput == nil || !r.hasOutput(resp) {
		return fmt.Errorf("%s step returned status ok without %s output", r.roleName, r.roleName)
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

func (r *roleAgent) updateTaskState(ctx agent.InvocationContext, resp *contracts.RawAgentResponse, _, _ int) error {
	if resp == nil {
		return fmt.Errorf("nil agent response for role %q", r.roleName)
	}

	state := r.runtime.getTaskState(ctx)
	if r.persist != nil {
		r.persist(state, resp)
	}

	if err := ctx.Session().State().Set("task_state", state); err != nil {
		return fmt.Errorf("set task state in session: %w", err)
	}

	return nil
}

func applyAgentResponseToTaskState(state *contracts.TaskState, resp *contracts.RawAgentResponse, role string) {
	spec, ok := roleAgentSpecByName(role)
	if !ok || spec.persist == nil {
		return
	}
	spec.persist(state, resp)
}

func persistRoleOutput(roleName string, output json.RawMessage, target *json.RawMessage) {
	if output == nil {
		return
	}
	var decoded json.RawMessage
	if err := json.Unmarshal(output, &decoded); err != nil {
		log.Warn().Err(err).Str("role", roleName).Msg("unmarshal role output to task state")
		return
	}
	*target = output
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

func ensureWorkspaceClean(ctx context.Context, workspaceDir, roleName string) error {
	statusOut, err := git.GitRunCmdOutput(ctx, workspaceDir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read workspace status after %s step: %w", roleName, err)
	}
	status := strings.TrimSpace(statusOut)
	if status == "" {
		return nil
	}
	return fmt.Errorf("%s step modified workspace; only do may leave workspace changes", roleName)
}
