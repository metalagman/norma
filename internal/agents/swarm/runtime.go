package swarm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/normahq/norma/v2/internal/adkrunner"
	"github.com/normahq/norma/v2/internal/apps/tasksmcp"
	"github.com/normahq/norma/v2/internal/config"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/normahq/runtime/v2/agentconfig"
	"github.com/normahq/runtime/v2/agentfactory"
	"github.com/normahq/runtime/v2/mcpregistry"
	"github.com/rs/zerolog"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/genai"
)

const tasksMCPServerName = "norma_tasks"

type Config struct {
	Logger     zerolog.Logger
	WorkingDir string
	Runtime    config.Config
	Roles      map[string]config.ResolvedSwarmRoleConfig
	Tracker    task.Tracker
}

type Runtime struct {
	logger         zerolog.Logger
	workingDir     string
	tracker        task.Tracker
	runDir         string
	providers      map[string]agentconfig.Config
	taskServer     *tasksmcp.HTTPServerResult
	roleByKey      map[string]*roleRuntime
	roleByAssignee map[string]*roleRuntime
	primaryRole    *roleRuntime
	runAgent       func(context.Context, *roleRuntime, runTaskInput) (string, error)
}

type roleRuntime struct {
	config config.ResolvedSwarmRoleConfig
	agent  agent.Agent
}

type runTaskInput struct {
	SessionID string
	CWD       string
	Prompt    string
}

type taskOutcome int

const (
	taskOutcomeCompleted taskOutcome = iota
	taskOutcomeHandedOff
	taskOutcomeBouncedToCoordinator
	taskOutcomeNeedsHumanTriage
	taskOutcomeNoProgress
)

func New(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.Tracker == nil {
		return nil, fmt.Errorf("tracker is required")
	}
	if len(cfg.Roles) == 0 {
		return nil, fmt.Errorf("roles are required")
	}
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	absWorkingDir, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory %q: %w", workingDir, err)
	}

	taskServer, err := startEmbeddedTaskServer(ctx, absWorkingDir, cfg.Tracker)
	if err != nil {
		return nil, err
	}

	servers := make(map[string]agentconfig.MCPServerConfig, len(cfg.Runtime.Runtime.MCPServers)+1)
	for name, serverCfg := range cfg.Runtime.Runtime.MCPServers {
		servers[name] = serverCfg
	}
	servers[tasksMCPServerName] = agentconfig.MCPServerConfig{
		Type: agentconfig.MCPServerTypeHTTP,
		URL:  fmt.Sprintf("http://%s", taskServer.Addr),
	}

	factory := agentfactory.New(cfg.Runtime.Runtime.Providers, mcpregistry.New(servers))

	runDir := filepath.Join(absWorkingDir, ".norma", "swarm", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		_ = taskServer.Close()
		return nil, fmt.Errorf("create swarm run dir: %w", err)
	}

	runtime := &Runtime{
		logger:         cfg.Logger.With().Str("component", "swarm").Logger(),
		workingDir:     absWorkingDir,
		tracker:        cfg.Tracker,
		runDir:         runDir,
		providers:      cfg.Runtime.Runtime.Providers,
		taskServer:     taskServer,
		roleByKey:      make(map[string]*roleRuntime, len(cfg.Roles)),
		roleByAssignee: make(map[string]*roleRuntime, len(cfg.Roles)),
	}
	runtime.runAgent = runtime.runTaskAgent

	for key, roleCfg := range cfg.Roles {
		roleAgent, err := factory.Build(ctx, agentfactory.BuildRequest{
			AgentID:          roleCfg.ProviderID,
			Name:             roleCfg.Assignee,
			Description:      "Norma swarm role agent",
			Instruction:      roleCfg.Instruction,
			WorkingDirectory: absWorkingDir,
		})
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("build swarm role %q with provider %q: %w", key, roleCfg.ProviderID, err)
		}
		roleRuntime := &roleRuntime{
			config: roleCfg,
			agent:  roleAgent,
		}
		runtime.roleByKey[key] = roleRuntime
		runtime.roleByAssignee[roleCfg.Assignee] = roleRuntime
		if roleCfg.IsPrimaryRole {
			runtime.primaryRole = roleRuntime
		}
	}
	if runtime.primaryRole == nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("primary swarm role is required")
	}

	return runtime, nil
}

func (r *Runtime) Close() error {
	var firstErr error
	for _, role := range r.roleByKey {
		if closer, ok := role.agent.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if r.taskServer != nil {
		if err := r.taskServer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Runtime) Run(ctx context.Context, epicID string) error {
	epicID = strings.TrimSpace(epicID)
	if epicID == "" {
		return fmt.Errorf("epic id is required")
	}
	epic, err := r.tracker.Task(ctx, epicID)
	if err != nil {
		return fmt.Errorf("load epic %q: %w", epicID, err)
	}
	if strings.TrimSpace(epic.Type) != "epic" {
		return fmt.Errorf("task %q is type %q, want epic", epicID, epic.Type)
	}

	skipped := make(map[string]string)
	for {
		leaves, err := r.tracker.LeafTasks(ctx)
		if err != nil {
			return fmt.Errorf("list ready leaf tasks: %w", err)
		}

		candidates, reports := r.selectCandidates(ctx, epicID, leaves, skipped)
		for _, report := range reports {
			r.logger.Info().Str("task_id", report.TaskID).Msg(report.Message)
		}
		if len(candidates) == 0 {
			r.logger.Info().Str("epic_id", epicID).Msg("swarm queue is empty")
			return nil
		}

		current := candidates[0]
		if err := r.executeCandidate(ctx, epicID, current, skipped); err != nil {
			r.logger.Error().Err(err).Str("task_id", current.task.ID).Str("assignee", current.task.Assignee).Msg("swarm task execution failed")
			skipped[current.task.ID] = err.Error()
		}
	}
}

type candidate struct {
	task    task.Task
	role    *roleRuntime
	inScope bool
}

type selectionReport struct {
	TaskID  string
	Message string
}

func (r *Runtime) selectCandidates(ctx context.Context, epicID string, leaves []task.Task, skipped map[string]string) ([]candidate, []selectionReport) {
	candidates := make([]candidate, 0, len(leaves))
	reports := make([]selectionReport, 0)
	for _, item := range leaves {
		if _, skip := skipped[item.ID]; skip {
			continue
		}
		role := r.roleByAssignee[strings.TrimSpace(item.Assignee)]
		inScope := r.taskInEpic(ctx, item, epicID)
		if role == nil {
			if inScope && strings.TrimSpace(item.Assignee) == "" {
				reports = append(reports, selectionReport{
					TaskID:  item.ID,
					Message: "ready task is unassigned; leaving it for human assignment",
				})
			}
			continue
		}
		if !inScope && strings.TrimSpace(item.Assignee) == "" {
			continue
		}
		candidates = append(candidates, candidate{
			task:    item,
			role:    role,
			inScope: inScope,
		})
	}

	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.task.Priority != b.task.Priority {
			return a.task.Priority - b.task.Priority
		}
		if cmp := strings.Compare(a.task.CreatedAt, b.task.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.task.ID, b.task.ID)
	})
	return candidates, reports
}

func (r *Runtime) executeCandidate(ctx context.Context, epicID string, item candidate, skipped map[string]string) error {
	if err := r.tracker.MarkStatus(ctx, item.task.ID, "doing"); err != nil {
		return fmt.Errorf("mark task doing: %w", err)
	}

	taskDir := filepath.Join(r.runDir, "tasks", item.task.ID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return fmt.Errorf("create task run dir: %w", err)
	}

	prompt := buildPrompt(r.workingDir, epicID, taskDir, item.task, item.role.config)
	if err := os.WriteFile(filepath.Join(taskDir, "prompt.txt"), []byte(prompt), 0o600); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	response, err := r.runAgent(ctx, item.role, runTaskInput{
		SessionID: fmt.Sprintf("swarm-%d-%s", time.Now().UTC().UnixNano(), item.task.ID),
		CWD:       r.workingDir,
		Prompt:    prompt,
	})
	if response != "" {
		_ = os.WriteFile(filepath.Join(taskDir, "response.txt"), []byte(response), 0o600)
	}
	if err != nil {
		_ = r.tracker.MarkStatus(ctx, item.task.ID, "todo")
		return err
	}

	after, err := r.tracker.Task(ctx, item.task.ID)
	if err != nil {
		return fmt.Errorf("reload task after role run: %w", err)
	}

	outcome, err := inferOutcome(item.task, after, r.primaryRole.config.Assignee)
	if err != nil {
		return err
	}

	switch outcome {
	case taskOutcomeCompleted:
		r.logger.Info().Str("task_id", item.task.ID).Msg("swarm task completed")
	case taskOutcomeHandedOff:
		if err := r.tracker.MarkStatus(ctx, item.task.ID, "todo"); err != nil {
			return fmt.Errorf("mark handed-off task todo: %w", err)
		}
		r.logger.Info().Str("task_id", item.task.ID).Str("assignee", after.Assignee).Msg("swarm task handed off")
	case taskOutcomeNeedsHumanTriage:
		if err := r.tracker.MarkStatus(ctx, item.task.ID, "todo"); err != nil {
			return fmt.Errorf("mark triage task todo: %w", err)
		}
		skipped[item.task.ID] = "task became unassigned after role execution"
		r.logger.Warn().Str("task_id", item.task.ID).Msg("task is unassigned after role execution; waiting for human triage")
	case taskOutcomeBouncedToCoordinator:
		if err := r.tracker.SetAssignee(ctx, item.task.ID, r.primaryRole.config.Assignee); err != nil {
			return fmt.Errorf("reassign task to coordinator: %w", err)
		}
		if err := r.tracker.MarkStatus(ctx, item.task.ID, "todo"); err != nil {
			return fmt.Errorf("mark bounced task todo: %w", err)
		}
		r.logger.Warn().Str("task_id", item.task.ID).Str("assignee", r.primaryRole.config.Assignee).Msg("task made no progress; bounced to coordinator")
	case taskOutcomeNoProgress:
		if err := r.tracker.MarkStatus(ctx, item.task.ID, "todo"); err != nil {
			return fmt.Errorf("mark unchanged task todo: %w", err)
		}
		skipped[item.task.ID] = "coordinator left task unchanged"
		r.logger.Warn().Str("task_id", item.task.ID).Msg("coordinator left task unchanged")
	}

	return nil
}

func (r *Runtime) runTaskAgent(ctx context.Context, role *roleRuntime, input runTaskInput) (string, error) {
	factory := agentfactory.New(map[string]agentconfig.Config{
		role.config.ProviderID: r.providers[role.config.ProviderID],
	}, mcpregistry.New(nil))
	state, err := factory.BuildSessionState(role.config.ProviderID, input.CWD)
	if err != nil {
		return "", fmt.Errorf("build session state: %w", err)
	}

	_, lastContent, err := adkrunner.Run(ctx, adkrunner.RunInput{
		AppName:        "norma",
		UserID:         "norma-swarm",
		SessionID:      input.SessionID,
		Agent:          role.agent,
		InitialState:   state,
		InitialContent: genai.NewContentFromText(input.Prompt, genai.RoleUser),
	})
	if err != nil {
		return "", fmt.Errorf("run role agent %q: %w", role.config.Assignee, err)
	}
	if lastContent == nil {
		return "", nil
	}
	var parts []string
	for _, part := range lastContent.Parts {
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func (r *Runtime) taskInEpic(ctx context.Context, item task.Task, epicID string) bool {
	current := strings.TrimSpace(item.ParentID)
	for current != "" {
		if current == epicID {
			return true
		}
		parent, err := r.tracker.Task(ctx, current)
		if err != nil {
			r.logger.Warn().Err(err).Str("task_id", item.ID).Str("parent_id", current).Msg("failed to resolve task ancestry")
			return false
		}
		current = strings.TrimSpace(parent.ParentID)
	}
	return item.ID == epicID
}

func startEmbeddedTaskServer(ctx context.Context, workingDir string, tracker task.Tracker) (*tasksmcp.HTTPServerResult, error) {
	trimmedWorkingDir := strings.TrimSpace(workingDir)
	if trimmedWorkingDir == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	absoluteWorkingDir, err := filepath.Abs(trimmedWorkingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory path %q: %w", trimmedWorkingDir, err)
	}
	if beadsTracker, ok := tracker.(*task.BeadsTracker); ok {
		beadsTracker.WorkingDir = absoluteWorkingDir
	}
	return tasksmcp.StartHTTPServer(ctx, tracker, "127.0.0.1:0")
}

func inferOutcome(before, after task.Task, primaryAssignee string) (taskOutcome, error) {
	if strings.TrimSpace(after.Status) == "done" {
		return taskOutcomeCompleted, nil
	}
	afterAssignee := strings.TrimSpace(after.Assignee)
	beforeAssignee := strings.TrimSpace(before.Assignee)
	if afterAssignee == "" {
		return taskOutcomeNeedsHumanTriage, nil
	}
	if afterAssignee != beforeAssignee {
		return taskOutcomeHandedOff, nil
	}
	if afterAssignee == strings.TrimSpace(primaryAssignee) {
		return taskOutcomeNoProgress, nil
	}
	return taskOutcomeBouncedToCoordinator, nil
}

func buildPrompt(workingDir, epicID, taskDir string, item task.Task, role config.ResolvedSwarmRoleConfig) string {
	var sections []string
	sections = append(sections,
		fmt.Sprintf("You are %s.", role.Assignee),
		"Operate as a swarm role agent. Use the Beads task MCP tools for coordination and state changes.",
		fmt.Sprintf("Task ID: %s", item.ID),
		fmt.Sprintf("Epic ID: %s", epicID),
		fmt.Sprintf("Working directory: %s", workingDir),
		fmt.Sprintf("Artifact directory: %s", taskDir),
	)
	if strings.TrimSpace(item.Title) != "" {
		sections = append(sections, "Title:\n"+strings.TrimSpace(item.Title))
	}
	if strings.TrimSpace(item.Goal) != "" {
		sections = append(sections, "Task:\n"+strings.TrimSpace(item.Goal))
	}
	sections = append(sections,
		"Rules:",
		"- Initial task assignment is human-owned. Do not pull unassigned work for yourself.",
		"- If you complete the task, close it in Beads.",
		"- If another role should take over, reassign the task in Beads.",
		"- If you create follow-up work, keep it under the right parent and assign it explicitly when needed.",
		"- If you cannot safely complete or reassign the task, leave it open and unchanged so the harness can escalate it.",
	)
	return strings.Join(sections, "\n\n")
}
