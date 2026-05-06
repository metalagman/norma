package taskmaster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultAgentType   = "codex_acp"
	defaultModel       = "gpt-5.3-codex"
	defaultQueueDepth  = 32
	initialGoalTaskID  = "goal-task"
	defaultMaxAttempts = 1
	rootAgentName      = "Taskmaster"
)

var childAgentInstructions = map[string]string{
	"plan": strings.Join([]string{
		"You are the plan phase of a strict PDCA flow.",
		"Work only on planning for the current iteration.",
		"Produce the next concise plan that the do phase should execute.",
		"Do not execute work, check results, or act on outcomes.",
		"Return only the useful planning result.",
	}, "\n"),
	"do": strings.Join([]string{
		"You are the do phase of a strict PDCA flow.",
		"Execute only the assigned plan for the current iteration.",
		"Do not replan, verify completion, or choose the next action.",
		"Return only the useful execution result for the check phase.",
	}, "\n"),
	"check": strings.Join([]string{
		"You are the check phase of a strict PDCA flow.",
		"Compare the execution result against the plan and the goal.",
		"Return lowercase `pass` only when the task is complete for this iteration.",
		"Otherwise return lowercase `fail` with concise evidence.",
		"Do not act, replan, or execute more work.",
	}, "\n"),
	"act": strings.Join([]string{
		"You are the act phase of a strict PDCA flow.",
		"Consume only the check result for the current iteration.",
		"If the verdict is `pass`, return lowercase `close`.",
		"If the verdict is `fail`, return lowercase `continue` or `replan` with a concise reason.",
		"Never return uppercase literals and never return `rollback`.",
	}, "\n"),
}

func childAgentIDs() []string {
	return []string{"plan", "do", "check", "act"}
}

func isKnownRuntimeAgentID(id string) bool {
	if id == taskmasterAgentID {
		return true
	}
	_, ok := childAgentInstructions[id]
	return ok
}

type Config struct {
	Goal       string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

type taskKind string

const (
	taskKindAgent      taskKind = "agent"
	taskKindTaskmaster taskKind = "taskmaster"
)

type taskStatus string

const (
	taskStatusQueued    taskStatus = "queued"
	taskStatusRunning   taskStatus = "running"
	taskStatusCompleted taskStatus = "completed"
	taskStatusFailed    taskStatus = "failed"
)

type task struct {
	ID            string
	Kind          taskKind
	Locator       taskLocator
	ReplyTo       taskLocator
	SourceTaskID  string
	SourceLocator *taskLocator
	Input         string
	Status        taskStatus
	Attempt       int
	MaxAttempts   int
	CreatedAt     time.Time
	ScheduledAt   time.Time
	ClaimedAt     time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	Output        string
	Error         string
}

type runResult struct {
	Summary string
}

type taskRunner interface {
	RunTask(ctx context.Context, taskID string, taskText string) (string, error)
}

type rootSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	sessionID      string
	logger         zerolog.Logger
}

type rootSessionConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Logger     zerolog.Logger
	MCPServers map[string]acpagent.MCPServerConfig
}

func newRootSession(ctx context.Context, cfg rootSessionConfig) (*rootSession, error) {
	logger := cfg.Logger.With().Str("agent_id", taskmasterAgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              rootAgentName,
		Description:       "Taskmaster async root agent",
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       rootInstruction(),
		MCPServers:        cfg.MCPServers,
	})
	if err != nil {
		return nil, fmt.Errorf("create taskmaster agent: %w", err)
	}
	sessionService := session.InMemoryService()
	const appName = "taskmaster-root"
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create taskmaster runner: %w", err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    taskmasterAgentID,
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create taskmaster session: %w", err)
	}
	return &rootSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        appName,
		sessionID:      created.Session.ID(),
		logger:         logger,
	}, nil
}

func (s *rootSession) RunTask(ctx context.Context, taskID string, taskText string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	taskLogger := s.logger.With().Str("task_id", taskID).Logger()
	_, last, err := runWithRunner(ctx, s.runner, s.sessionService, s.appName, taskmasterAgentID, s.sessionID, taskText, func(output string) {
		taskLogger.Debug().Str("output", output).Msg("task output")
	})
	return last, err
}

func (s *rootSession) close() {
	if s.agent != nil {
		_ = s.agent.Close()
	}
}

type childAgentSetConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Logger     zerolog.Logger
}

type childAgentSet struct {
	agents map[string]*childAgentSession
}

func newChildAgentSet(ctx context.Context, cfg childAgentSetConfig) (*childAgentSet, error) {
	agents := make(map[string]*childAgentSession, len(childAgentInstructions))
	for agentID, instruction := range childAgentInstructions {
		child, err := newChildAgentSession(ctx, childAgentSessionConfig{
			AgentID:     agentID,
			Instruction: instruction,
			Command:     cfg.Command,
			WorkingDir:  cfg.WorkingDir,
			Stderr:      cfg.Stderr,
			Logger:      cfg.Logger,
		})
		if err != nil {
			for _, created := range agents {
				created.close()
			}
			return nil, err
		}
		agents[agentID] = child
	}
	return &childAgentSet{agents: agents}, nil
}

func (s *childAgentSet) close() {
	for _, child := range s.agents {
		child.close()
	}
}

type childAgentSessionConfig struct {
	AgentID     string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
}

type childAgentSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	sessionID      string
	logger         zerolog.Logger
}

func newChildAgentSession(ctx context.Context, cfg childAgentSessionConfig) (*childAgentSession, error) {
	name := "Taskmaster" + strings.ToUpper(cfg.AgentID[:1]) + cfg.AgentID[1:]
	logger := cfg.Logger.With().Str("agent_id", cfg.AgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              name,
		Description:       "Taskmaster " + cfg.AgentID + " child agent",
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       cfg.Instruction,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s child agent: %w", cfg.AgentID, err)
	}
	appName := "taskmaster-" + cfg.AgentID
	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s child agent runner: %w", cfg.AgentID, err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    taskmasterAgentID,
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s child agent session: %w", cfg.AgentID, err)
	}
	return &childAgentSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        appName,
		sessionID:      created.Session.ID(),
		logger:         logger,
	}, nil
}

func (s *childAgentSession) RunTask(ctx context.Context, taskID string, taskText string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	taskLogger := s.logger.With().Str("task_id", taskID).Logger()
	_, last, err := runWithRunner(ctx, s.runner, s.sessionService, s.appName, taskmasterAgentID, s.sessionID, taskText, func(output string) {
		taskLogger.Debug().Str("output", output).Msg("task output")
	})
	return last, err
}

func (s *childAgentSession) close() {
	if s.agent != nil {
		_ = s.agent.Close()
	}
}

type executor struct {
	agentID     string
	queue       <-chan *task
	runner      taskRunner
	coordinator *coordinator
	logger      zerolog.Logger
}

func (e *executor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case nextTask := <-e.queue:
			if nextTask == nil {
				continue
			}
			if nextTask.Kind == taskKindTaskmaster && nextTask.SourceTaskID != "" {
				e.logger.Debug().
					Str("task_id", nextTask.ID).
					Str("source_task_id", nextTask.SourceTaskID).
					Interface("source_locator", nextTask.SourceLocator).
					Interface("reply_to", nextTask.ReplyTo).
					Msg("notification task received")
			}
			e.logger.Info().
				Str("task_id", nextTask.ID).
				Str("task", nextTask.Input).
				Msg("agent received task")
			e.coordinator.markTaskStarted(nextTask)
			output, err := e.runner.RunTask(ctx, nextTask.ID, nextTask.Input)
			if err != nil {
				e.logger.Info().
					Str("task_id", nextTask.ID).
					Str("error", err.Error()).
					Msg("agent finished task")
			} else {
				e.logger.Info().
					Str("task_id", nextTask.ID).
					Str("result", strings.TrimSpace(output)).
					Msg("agent finished task")
			}
			e.coordinator.handleTaskResult(nextTask, output, err)
		}
	}
}

type coordinator struct {
	logger zerolog.Logger

	mu           sync.Mutex
	tasks        map[string]*task
	queues       map[string]chan *task
	terminal     bool
	finalSummary string
	finalErr     error
	done         chan runResult
	doneClosed   bool

	wg sync.WaitGroup
}

func newCoordinator(logger zerolog.Logger, runners map[string]taskRunner) (*coordinator, error) {
	for _, agentID := range append([]string{taskmasterAgentID}, childAgentIDs()...) {
		if runners[agentID] == nil {
			return nil, fmt.Errorf("missing runner for agent %q", agentID)
		}
	}
	c := &coordinator{
		logger: logger,
		tasks:  make(map[string]*task),
		queues: make(map[string]chan *task),
		done:   make(chan runResult, 1),
	}
	for agentID := range runners {
		c.queues[agentID] = make(chan *task, defaultQueueDepth)
	}
	return c, nil
}

func (c *coordinator) start(ctx context.Context, runners map[string]taskRunner) {
	for agentID, runner := range runners {
		exec := &executor{
			agentID:     agentID,
			queue:       c.queues[agentID],
			runner:      runner,
			coordinator: c,
			logger:      c.logger.With().Str("agent_id", agentID).Logger(),
		}
		c.wg.Add(1)
		go func(e *executor) {
			defer c.wg.Done()
			e.run(ctx)
		}(exec)
	}
}

func (c *coordinator) wait() {
	c.wg.Wait()
}

func (c *coordinator) enqueueInitialGoal(goal string) error {
	initial := &task{
		ID:          initialGoalTaskID,
		Kind:        taskKindTaskmaster,
		Locator:     newAgentLocator(taskmasterAgentID),
		ReplyTo:     newAgentLocator(taskmasterAgentID),
		Input:       formatInitialGoalTaskInput(goal),
		Status:      taskStatusQueued,
		Attempt:     1,
		MaxAttempts: defaultMaxAttempts,
	}
	return c.enqueueTask(initial)
}

func (c *coordinator) scheduleTask(taskID string, locator taskLocator, replyTo taskLocator, taskText string) error {
	taskID = strings.TrimSpace(taskID)
	taskText = strings.TrimSpace(taskText)
	if taskID == "" {
		return errors.New("task_id is required")
	}
	if taskText == "" {
		return errors.New("task is required")
	}
	if locator.Type != locatorTypeAgent {
		return fmt.Errorf("unsupported locator.type %q", locator.Type)
	}
	if _, ok := childAgentInstructions[locator.ID]; !ok {
		return fmt.Errorf("unknown child agent locator.id %q", locator.ID)
	}
	if replyTo.Type != locatorTypeAgent {
		return fmt.Errorf("unsupported reply_to.type %q", replyTo.Type)
	}
	if !isKnownRuntimeAgentID(replyTo.ID) {
		return fmt.Errorf("unknown reply_to.id %q", replyTo.ID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	if _, exists := c.tasks[taskID]; exists {
		return fmt.Errorf("task %q already exists", taskID)
	}
	queued := c.newQueuedTaskLocked(taskID, taskKindAgent, locator, replyTo, taskText)
	return c.enqueueQueuedTaskLocked(queued)
}

func (c *coordinator) finish(summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return errors.New("summary is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return nil
	}
	c.terminal = true
	c.finalSummary = summary
	return nil
}

func (c *coordinator) waitResult(ctx context.Context) (runResult, error) {
	select {
	case <-ctx.Done():
		return runResult{}, ctx.Err()
	case result := <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalErr != nil {
			return runResult{}, c.finalErr
		}
		return result, nil
	}
}

func (c *coordinator) setRunError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doneClosed {
		return
	}
	c.terminal = true
	c.finalErr = err
	c.sendDoneLocked(runResult{})
}

func (c *coordinator) enqueueTask(nextTask *task) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	if _, exists := c.tasks[nextTask.ID]; exists {
		return fmt.Errorf("task %q already exists", nextTask.ID)
	}
	queued := c.newQueuedTaskLocked(nextTask.ID, nextTask.Kind, nextTask.Locator, nextTask.ReplyTo, nextTask.Input)
	queued.SourceTaskID = nextTask.SourceTaskID
	queued.SourceLocator = nextTask.SourceLocator
	return c.enqueueQueuedTaskLocked(queued)
}

func (c *coordinator) newQueuedTaskLocked(taskID string, kind taskKind, locator taskLocator, replyTo taskLocator, input string) *task {
	now := time.Now().UTC()
	nextTask := &task{
		ID:          taskID,
		Kind:        kind,
		Locator:     locator,
		ReplyTo:     replyTo,
		Input:       input,
		Status:      taskStatusQueued,
		Attempt:     1,
		MaxAttempts: defaultMaxAttempts,
		CreatedAt:   now,
		ScheduledAt: now,
	}
	c.tasks[taskID] = nextTask
	return nextTask
}

func (c *coordinator) enqueueQueuedTaskLocked(nextTask *task) error {
	queue, ok := c.queues[nextTask.Locator.ID]
	if !ok {
		return fmt.Errorf("unknown locator.id %q", nextTask.Locator.ID)
	}
	c.logTaskEvent("task enqueued", nextTask)
	queue <- nextTask
	return nil
}

func (c *coordinator) markTaskStarted(nextTask *task) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	nextTask.ClaimedAt = now
	nextTask.StartedAt = now
	nextTask.Status = taskStatusRunning
	c.logTaskEvent("task started", nextTask)
}

func (c *coordinator) handleTaskResult(doneTask *task, output string, err error) {
	c.mu.Lock()
	now := time.Now().UTC()
	doneTask.FinishedAt = now
	doneTask.Output = strings.TrimSpace(output)
	var notification *task
	if err != nil {
		doneTask.Status = taskStatusFailed
		doneTask.Error = err.Error()
		c.logTaskEvent("task failed", doneTask)
	} else {
		doneTask.Status = taskStatusCompleted
		c.logTaskEvent("task completed", doneTask)
	}

	if doneTask.Locator.ID == taskmasterAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("taskmaster task %q failed: %w", doneTask.ID, err)
			c.terminal = true
			c.sendDoneLocked(runResult{})
			c.mu.Unlock()
			return
		}
		if c.terminal {
			c.sendDoneLocked(runResult{Summary: c.finalSummary})
		}
		c.mu.Unlock()
		return
	}

	if !c.terminal {
		sourceLocator := doneTask.Locator
		notification = c.newQueuedTaskLocked(
			doneTask.ID+".notify",
			taskKindTaskmaster,
			doneTask.ReplyTo,
			newAgentLocator(taskmasterAgentID),
			formatNotificationTaskInput(doneTask),
		)
		notification.SourceTaskID = doneTask.ID
		notification.SourceLocator = &sourceLocator
		c.logTaskEvent("notification task created", notification)
	}
	c.mu.Unlock()

	if notification != nil {
		if err := c.enqueueNotification(notification); err != nil {
			c.setRunError(fmt.Errorf("enqueue notification for %q: %w", doneTask.ID, err))
		}
	}
}

func (c *coordinator) enqueueNotification(nextTask *task) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	queue, ok := c.queues[nextTask.Locator.ID]
	if !ok {
		return fmt.Errorf("unknown locator.id %q", nextTask.Locator.ID)
	}
	c.logTaskEvent("task enqueued", nextTask)
	queue <- nextTask
	return nil
}

func (c *coordinator) logTaskEvent(message string, nextTask *task) {
	event := c.logger.Debug().
		Str("task_id", nextTask.ID).
		Str("kind", string(nextTask.Kind)).
		Interface("locator", nextTask.Locator).
		Interface("reply_to", nextTask.ReplyTo).
		Str("status", string(nextTask.Status))
	if nextTask.SourceTaskID != "" {
		event = event.Str("source_task_id", nextTask.SourceTaskID)
	}
	if nextTask.SourceLocator != nil {
		event = event.Interface("source_locator", nextTask.SourceLocator)
	}
	if nextTask.Output != "" {
		event = event.Str("output", nextTask.Output)
	}
	if nextTask.Error != "" {
		event = event.Str("error", nextTask.Error)
	}
	event.Msg(message)
}

func (c *coordinator) sendDoneLocked(result runResult) {
	if c.doneClosed {
		return
	}
	c.done <- result
	c.doneClosed = true
}

func formatNotificationTaskInput(doneTask *task) string {
	type completionEnvelope struct {
		Type          string      `json:"type"`
		Phase         string      `json:"phase"`
		SourceTaskID  string      `json:"source_task_id"`
		SourceLocator taskLocator `json:"source_locator"`
		ReplyTo       taskLocator `json:"reply_to"`
		Status        string      `json:"status"`
		Result        string      `json:"result,omitempty"`
		Error         string      `json:"error,omitempty"`
	}

	envelope := completionEnvelope{
		Type:          "task_completion",
		Phase:         doneTask.Locator.ID,
		SourceTaskID:  doneTask.ID,
		SourceLocator: doneTask.Locator,
		ReplyTo:       doneTask.ReplyTo,
		Status:        string(doneTask.Status),
	}
	if doneTask.Error != "" {
		envelope.Error = doneTask.Error
	} else {
		envelope.Result = doneTask.Output
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return strings.TrimSpace(fmt.Sprintf("TASK ENVELOPE:\n%s", doneTask.ID))
	}
	return strings.Join([]string{
		"TASK ENVELOPE:",
		"This is the completion of one strict PDCA phase. Use it to choose the next phase in order.",
		string(payload),
	}, "\n")
}

func formatInitialGoalTaskInput(goal string) string {
	return strings.Join([]string{
		"GOAL TASK:",
		strings.TrimSpace(goal),
		"",
		"PDCA MODE:",
		"- Run strict PDCA iterations in this exact order: plan -> do -> check -> act.",
		"- Start with plan for iteration 1.",
		"- The check phase returns lowercase `pass` or `fail`.",
		"- The act phase returns lowercase `close`, `continue`, or `replan`.",
		"- If act returns `close`, call taskmaster.finish.",
		"- If act returns `continue`, start the next iteration from plan.",
		"- If act returns `replan`, call taskmaster.finish with a concise replan summary.",
	}, "\n")
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the Taskmaster async root agent named taskmaster.",
		"You receive taskmaster tasks in one of two forms: GOAL TASK or TASK ENVELOPE.",
		"You are running a strict PDCA workflow over child agents.",
		"Run phases in this exact order for each iteration: plan -> do -> check -> act.",
		"Always start a new goal with plan. Do not skip phases and do not reorder them.",
		"Use only the taskmaster.schedule_task tool to enqueue child-agent tasks, and taskmaster.finish to finish the run.",
		"Each scheduled task must include a stable task_id, a locator, an optional reply_to, and task text.",
		"The child agents available in this MVP are plan, do, check, and act.",
		"Treat plan, do, check, and act as strict PDCA phases, not generic workers.",
		"After a plan completion, schedule do. After a do completion, schedule check. After a check completion, schedule act.",
		"The check phase returns lowercase `pass` or `fail`.",
		"The act phase returns lowercase `close`, `continue`, or `replan`.",
		"If act returns `close`, call taskmaster.finish with a concise final summary.",
		"If act returns `continue`, start the next PDCA iteration from plan.",
		"If act returns `replan`, call taskmaster.finish with a concise replan-required summary. This MVP has no replacement-work tool.",
		"If a task envelope reports an error and you want to stop, call taskmaster.finish with a concise failure summary.",
		"Do not perform worker work yourself. Only coordinate the PDCA flow through child-agent tasks.",
		"Do not try to deliver work directly without using taskmaster.schedule_task.",
	}, "\n")
}

func Run(ctx context.Context, cfg Config) error {
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		workingDir = "."
	}
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdout = &syncWriter{writer: stdout}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stderr = &syncWriter{writer: stderr}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		baseLogger = zerolog.Ctx(ctx)
	}
	logger := baseLogger.With().
		Str("component", "playground.taskmaster").
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	childAgents, err := newChildAgentSet(ctx, childAgentSetConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	defer childAgents.close()

	serviceLogger := logger.With().Str("surface", "taskmaster").Logger()
	service := newService(serviceLogger)
	server, err := startHTTPServer(ctx, service, "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	root, err := newRootSession(ctx, rootSessionConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"taskmaster": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		return err
	}
	defer root.close()

	runners := map[string]taskRunner{
		taskmasterAgentID: root,
		"plan":            childAgents.agents["plan"],
		"do":              childAgents.agents["do"],
		"check":           childAgents.agents["check"],
		"act":             childAgents.agents["act"],
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	coordinator, err := newCoordinator(logger, runners)
	if err != nil {
		cancel()
		return err
	}
	service.coordinator = coordinator
	coordinator.start(runCtx, runners)

	logger.Info().Str("goal", goal).Msg("taskmaster started")
	if err := coordinator.enqueueInitialGoal(goal); err != nil {
		cancel()
		coordinator.wait()
		return err
	}
	result, err := coordinator.waitResult(ctx)
	cancel()
	coordinator.wait()
	if err != nil {
		return err
	}

	logger.Info().
		Bool("has_result", strings.TrimSpace(result.Summary) != "").
		Str("result", result.Summary).
		Msg("taskmaster completed")
	if result.Summary != "" {
		if _, err := fmt.Fprintln(stdout, result.Summary); err != nil {
			return err
		}
	}
	return nil
}

func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func runWithRunner(
	ctx context.Context,
	runner *adkrunner.Runner,
	sessionService session.Service,
	appName string,
	userID string,
	sessionID string,
	prompt string,
	onOutput func(string),
) (session.Session, string, error) {
	var lastContent *genai.Content
	events := runner.Run(ctx, userID, sessionID, genai.NewContentFromText(prompt, genai.RoleUser), agent.RunConfig{})
	for ev, runErr := range events {
		if runErr != nil {
			return nil, "", runErr
		}
		if ev != nil && ev.Content != nil {
			lastContent = ev.Content
			output := contentText(ev.Content)
			if onOutput != nil && output != "" {
				onOutput(output)
			}
		}
	}
	finalSession, err := sessionService.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, "", err
	}
	return finalSession.Session, contentText(lastContent), nil
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

type httpServerResult struct {
	Addr  string
	Close func() error
}

func startGenericHTTPServer(ctx context.Context, addr string, serverFactory func(*http.Request) *mcp.Server) (*httpServerResult, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}
	handler := mcp.NewStreamableHTTPHandler(serverFactory, &mcp.StreamableHTTPOptions{})
	httpServer := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return &httpServerResult{
		Addr: listener.Addr().String(),
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

func autoAllowPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
