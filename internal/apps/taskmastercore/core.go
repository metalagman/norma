package taskmastercore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
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
	defaultAgentType  = "codex_acp"
	defaultModel      = "gpt-5.3-codex"
	defaultQueueDepth = 32
	initialGoalTaskID = "goal-task"
)

type AgentConfig struct {
	Name        string
	Description string
	Instruction string
}

type IngressRequest struct {
	ID        string
	SessionID string
	Prompt    string
	Source    Locator
	ReportTo  *Locator
}

type CompletionPromptInput struct {
	TaskID       string
	SessionID    string
	SourceTaskID string
	Source       Locator
	Status       string
	Output       string
	Error        string
}

type RunResult struct {
	Summary string
}

type BackgroundGoalSource func(ctx context.Context, enqueue func(IngressRequest) error)

type Config struct {
	Goal          string
	WorkingDir    string
	BridgeBin     string
	Stdout        io.Writer
	Stderr        io.Writer
	Logger        *zerolog.Logger
	ComponentName string
	SurfaceName   string

	RootAgentID               string
	RootAgent                 AgentConfig
	ChildAgents               map[string]AgentConfig
	DefaultReportTo           Locator
	AllowFinishTool           bool
	FinishOnContextDone       bool
	Providers                 []Provider
	IngressPromptFormatter    func(IngressRequest) string
	CompletionPromptFormatter func(CompletionPromptInput) string
	BackgroundGoalSource      BackgroundGoalSource
}

type taskStatus string

const (
	taskStatusQueued     taskStatus = "queued"
	taskStatusRunning    taskStatus = "running"
	taskStatusCompleted  taskStatus = "completed"
	taskStatusDispatched taskStatus = "dispatched"
	taskStatusFailed     taskStatus = "failed"
)

type task struct {
	ID            string
	SessionID     string
	Locator       Locator
	ReportTo      Locator
	SourceTaskID  string
	SourceLocator *Locator
	Prompt        string
	Status        taskStatus
	CreatedAt     time.Time
	ScheduledAt   time.Time
	ClaimedAt     time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
	Output        string
	Error         string
}

type taskRunner interface {
	RunTask(ctx context.Context, sessionID string, taskID string, taskText string) (string, error)
}

type childInvoker interface {
	RunTask(ctx context.Context, sessionID string, callID string, prompt string) (string, error)
}

type closableRunner interface {
	childInvoker
	Close() error
}

type runnerSet interface {
	Runner(agentID string) childInvoker
	Close() error
}

type acpSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	userID         string
	logger         zerolog.Logger
	sessions       map[string]string
}

type acpSessionConfig struct {
	AgentID     string
	AppName     string
	Name        string
	Description string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
	UserID      string
	MCPServers  map[string]acpagent.MCPServerConfig
}

type childSessionSet struct {
	agents map[string]closableRunner
}

type childSessionSetConfig struct {
	RootAgentID string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
	ChildAgents map[string]AgentConfig
}

type runtimeDeps struct {
	newRootSession func(context.Context, acpSessionConfig) (closableRunner, error)
	newChildSet    func(context.Context, childSessionSetConfig) (runnerSet, error)
	startServer    func(context.Context, *service, string) (*httpServerResult, error)
}

type executor struct {
	agentID     string
	queue       <-chan *task
	runner      taskRunner
	coordinator *coordinator
	logger      zerolog.Logger
}

type coordinator struct {
	logger zerolog.Logger

	rootAgentID               string
	childAgentIDs             map[string]struct{}
	defaultReportTo           Locator
	allowFinishTool           bool
	finishOnContextDone       bool
	ingressPromptFormatter    func(IngressRequest) string
	completionPromptFormatter func(CompletionPromptInput) string
	providers                 providerRegistry

	mu               sync.Mutex
	tasks            map[string]*task
	queues           map[string]chan *task
	dispatchQueue    chan *task
	terminal         bool
	shuttingDown     bool
	finalSummary     string
	latestRootOutput string
	finalErr         error
	done             chan RunResult
	doneClosed       bool

	wg sync.WaitGroup
}

type httpServerResult struct {
	Addr  string
	Close func() error
}

type Runtime struct {
	logger      zerolog.Logger
	surfaceName string
	stdout      io.Writer
	startedAt   time.Time

	runCtx      context.Context
	cancel      context.CancelFunc
	coordinator *coordinator
	root        closableRunner
	children    runnerSet
	server      *httpServerResult

	closeOnce sync.Once
	closeErr  error
}

func defaultDeps() runtimeDeps {
	return runtimeDeps{
		newRootSession: func(ctx context.Context, cfg acpSessionConfig) (closableRunner, error) {
			return newACPSession(ctx, cfg)
		},
		newChildSet: newChildSessions,
		startServer: startHTTPServer,
	}
}

func Run(ctx context.Context, cfg Config) error {
	runtime, err := Start(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()

	runtime.logger.Info().Str("goal", strings.TrimSpace(cfg.Goal)).Msg(runtime.surfaceName + " started")
	if err := runtime.Ingest(IngressRequest{
		ID:        initialGoalTaskID,
		SessionID: initialGoalTaskID,
		Prompt:    strings.TrimSpace(cfg.Goal),
		Source:    NewCLILocator(initialGoalTaskID),
	}); err != nil {
		return err
	}
	if cfg.BackgroundGoalSource != nil {
		go cfg.BackgroundGoalSource(runtime.runCtx, func(backgroundGoal IngressRequest) error {
			return runtime.Ingest(backgroundGoal)
		})
	}
	result, err := runtime.Wait(ctx)
	if err != nil {
		return err
	}
	return runtime.WriteOutput(result)
}

func Start(ctx context.Context, cfg Config) (*Runtime, error) {
	return start(ctx, cfg, defaultDeps())
}

func start(ctx context.Context, cfg Config, deps runtimeDeps) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
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
	componentName := strings.TrimSpace(cfg.ComponentName)
	if componentName == "" {
		componentName = "playground.taskmaster"
	}
	surfaceName := strings.TrimSpace(cfg.SurfaceName)
	if surfaceName == "" {
		surfaceName = "taskmaster"
	}
	logger := baseLogger.With().
		Str("component", componentName).
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	children, err := deps.newChildSet(ctx, childSessionSetConfig{
		RootAgentID: cfg.RootAgentID,
		Command:     command,
		WorkingDir:  workingDir,
		Stderr:      stderr,
		Logger:      logger,
		ChildAgents: cfg.ChildAgents,
	})
	if err != nil {
		return nil, err
	}

	serviceLogger := logger.With().Str("surface", surfaceName).Logger()
	service := newService(serviceLogger, cfg.DefaultReportTo, cfg.AllowFinishTool)
	server, err := deps.startServer(ctx, service, "127.0.0.1:0")
	if err != nil {
		_ = children.Close()
		return nil, err
	}

	root, err := deps.newRootSession(ctx, acpSessionConfig{
		AgentID:     cfg.RootAgentID,
		AppName:     "taskmaster-" + cfg.RootAgentID,
		Name:        cfg.RootAgent.Name,
		Description: cfg.RootAgent.Description,
		Instruction: cfg.RootAgent.Instruction,
		Command:     command,
		WorkingDir:  workingDir,
		Stderr:      stderr,
		Logger:      logger,
		UserID:      cfg.RootAgentID,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"taskmaster": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		_ = server.Close()
		_ = children.Close()
		return nil, err
	}

	runners := make(map[string]taskRunner, len(cfg.ChildAgents)+1)
	runners[cfg.RootAgentID] = root
	for agentID := range cfg.ChildAgents {
		runners[agentID] = children.Runner(agentID)
	}

	runCtx, cancel := context.WithCancel(ctx)

	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		cancel()
		_ = root.Close()
		_ = server.Close()
		_ = children.Close()
		return nil, err
	}
	service.coordinator = coordinator
	coordinator.start(runCtx, runners)
	return &Runtime{
		logger:      logger,
		surfaceName: surfaceName,
		stdout:      stdout,
		startedAt:   time.Now(),
		runCtx:      runCtx,
		cancel:      cancel,
		coordinator: coordinator,
		root:        root,
		children:    children,
		server:      server,
	}, nil
}

func (r *Runtime) Ingest(req IngressRequest) error {
	return r.coordinator.ingest(req)
}

func (r *Runtime) Wait(ctx context.Context) (RunResult, error) {
	result, err := r.coordinator.waitResult(ctx)
	r.coordinator.beginShutdown()
	_ = r.Close()
	if err != nil {
		return RunResult{}, err
	}
	return result, nil
}

func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		r.cancel()
		r.coordinator.wait()
		var errs []string
		if r.root != nil {
			if err := r.root.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if r.server != nil {
			if err := r.server.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if r.children != nil {
			if err := r.children.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			r.closeErr = errors.New(strings.Join(errs, "; "))
		}
	})
	return r.closeErr
}

func (r *Runtime) WriteOutput(result RunResult) error {
	elapsed := formatElapsed(time.Since(r.startedAt))
	r.logger.Info().
		Bool("has_result", strings.TrimSpace(result.Summary) != "").
		Str("elapsed", elapsed).
		Str("result", result.Summary).
		Msg(r.surfaceName + " completed")
	return writeRunOutput(r.stdout, result.Summary, elapsed)
}

func validateConfig(cfg Config) error {
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	if strings.TrimSpace(cfg.RootAgentID) == "" {
		return errors.New("root agent id is required")
	}
	if strings.TrimSpace(cfg.RootAgent.Name) == "" {
		return errors.New("root agent name is required")
	}
	if strings.TrimSpace(cfg.RootAgent.Instruction) == "" {
		return errors.New("root agent instruction is required")
	}
	for childID, child := range cfg.ChildAgents {
		if strings.TrimSpace(childID) == "" {
			return errors.New("child agent id is required")
		}
		if strings.TrimSpace(child.Name) == "" {
			return fmt.Errorf("child agent %q name is required", childID)
		}
		if strings.TrimSpace(child.Instruction) == "" {
			return fmt.Errorf("child agent %q instruction is required", childID)
		}
	}
	defaultReportTo, err := normalizeLocator(cfg.DefaultReportTo)
	if err != nil {
		return fmt.Errorf("default report_to: %w", err)
	}
	if defaultReportTo.Type != LocatorTypeAgent || defaultReportTo.Kind != LocatorKindLocal {
		return fmt.Errorf("default report_to must be the local root agent locator")
	}
	if defaultReportTo.ID != strings.ToLower(strings.TrimSpace(cfg.RootAgentID)) {
		return fmt.Errorf("default report_to.id %q must match root agent id %q", defaultReportTo.ID, cfg.RootAgentID)
	}
	if cfg.IngressPromptFormatter == nil {
		return errors.New("ingress prompt formatter is required")
	}
	if cfg.CompletionPromptFormatter == nil {
		return errors.New("completion prompt formatter is required")
	}
	return nil
}

func newCoordinator(logger zerolog.Logger, cfg Config) (*coordinator, error) {
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))
	if cfg.IngressPromptFormatter == nil {
		return nil, errors.New("ingress prompt formatter is required")
	}
	if cfg.CompletionPromptFormatter == nil {
		return nil, errors.New("completion prompt formatter is required")
	}
	childIDs := make(map[string]struct{}, len(cfg.ChildAgents))
	runnerCount := len(cfg.ChildAgents) + 1
	c := &coordinator{
		logger:                    logger,
		rootAgentID:               rootID,
		childAgentIDs:             childIDs,
		defaultReportTo:           cfg.DefaultReportTo,
		allowFinishTool:           cfg.AllowFinishTool,
		finishOnContextDone:       cfg.FinishOnContextDone,
		ingressPromptFormatter:    cfg.IngressPromptFormatter,
		completionPromptFormatter: cfg.CompletionPromptFormatter,
		providers:                 newProviderRegistry(cfg.Providers),
		tasks:                     make(map[string]*task),
		queues:                    make(map[string]chan *task, runnerCount),
		dispatchQueue:             make(chan *task, defaultQueueDepth),
		done:                      make(chan RunResult, 1),
	}
	c.queues[rootID] = make(chan *task, defaultQueueDepth)
	for childID := range cfg.ChildAgents {
		normalized := strings.ToLower(strings.TrimSpace(childID))
		childIDs[normalized] = struct{}{}
		c.queues[normalized] = make(chan *task, defaultQueueDepth)
	}
	return c, nil
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
			if nextTask.SourceTaskID != "" {
				e.logger.Debug().
					Str("task_id", nextTask.ID).
					Str("source_task_id", nextTask.SourceTaskID).
					Interface("source_locator", nextTask.SourceLocator).
					Interface("report_to", nextTask.ReportTo).
					Msg("notification task received")
			}
			if !e.coordinator.tryStartTask(nextTask) {
				continue
			}
			e.logger.Info().
				Str("agent_id", e.agentID).
				Str("task_id", nextTask.ID).
				Str("session_id", nextTask.SessionID).
				Str("task", nextTask.Prompt).
				Msg("agent received task")
			output, err := e.runner.RunTask(ctx, nextTask.SessionID, nextTask.ID, nextTask.Prompt)
			if err != nil {
				e.logger.Info().
					Str("agent_id", e.agentID).
					Str("task_id", nextTask.ID).
					Str("session_id", nextTask.SessionID).
					Str("error", err.Error()).
					Msg("agent finished task")
			} else {
				e.logger.Info().
					Str("agent_id", e.agentID).
					Str("task_id", nextTask.ID).
					Str("session_id", nextTask.SessionID).
					Str("result", strings.TrimSpace(output)).
					Msg("agent finished task")
			}
			e.coordinator.handleTaskResult(nextTask, output, err)
		}
	}
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
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runDispatchLoop(ctx)
	}()
}

func (c *coordinator) wait() {
	c.wg.Wait()
}

func (c *coordinator) ingest(req IngressRequest) error {
	taskID := strings.TrimSpace(req.ID)
	sessionID := strings.TrimSpace(req.SessionID)
	prompt := strings.TrimSpace(req.Prompt)
	source, sourceErr := normalizeLocator(req.Source)
	reportTo, reportErr := normalizeReportLocator(req.ReportTo, c.defaultReportTo)
	if taskID == "" {
		return errors.New("id is required")
	}
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if prompt == "" {
		return errors.New("prompt is required")
	}
	if sourceErr != nil {
		return fmt.Errorf("source: %w", sourceErr)
	}
	if reportErr != nil {
		return reportErr
	}
	if err := c.validateReportTo(reportTo); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	queued := c.newQueuedTaskLocked(taskID, sessionID, NewAgentLocator(c.rootAgentID), reportTo, c.ingressPromptFormatter(req))
	queued.SourceLocator = &source
	return c.enqueueQueuedTaskLocked(queued)
}

func (c *coordinator) scheduleTask(taskID string, sessionID string, locator Locator, reportTo Locator, prompt string) error {
	taskID = strings.TrimSpace(taskID)
	sessionID = strings.TrimSpace(sessionID)
	prompt = strings.TrimSpace(prompt)
	if taskID == "" {
		return errors.New("task_id is required")
	}
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if prompt == "" {
		return errors.New("prompt is required")
	}
	if !c.isLocalChildTarget(locator) && !c.providers.supportsTarget(locator) {
		return fmt.Errorf("unsupported target locator %s", locatorKey(locator))
	}
	if err := c.validateReportTo(reportTo); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	if _, exists := c.tasks[taskID]; exists {
		return fmt.Errorf("task %q already exists", taskID)
	}
	queued := c.newQueuedTaskLocked(taskID, sessionID, locator, reportTo, prompt)
	return c.enqueueQueuedTaskLocked(queued)
}

func (c *coordinator) validateReportTo(reportTo Locator) error {
	if c.isLocalAgentTarget(reportTo) {
		return nil
	}
	if c.providers.supportsReport(reportTo) {
		return nil
	}
	return fmt.Errorf("unsupported report_to locator %s", locatorKey(reportTo))
}

func (c *coordinator) finish(summary string) error {
	if !c.allowFinishTool {
		return errors.New("finish tool is disabled for this run")
	}
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

func (c *coordinator) beginShutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shuttingDown = true
}

func (c *coordinator) waitResult(ctx context.Context) (RunResult, error) {
	select {
	case <-ctx.Done():
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalErr != nil {
			return RunResult{}, c.finalErr
		}
		if c.finishOnContextDone {
			return RunResult{Summary: strings.TrimSpace(c.latestRootOutput)}, nil
		}
		return RunResult{}, ctx.Err()
	case result := <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalErr != nil {
			return RunResult{}, c.finalErr
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
	c.sendDoneLocked(RunResult{})
}

func (c *coordinator) newQueuedTaskLocked(taskID string, sessionID string, locator Locator, reportTo Locator, prompt string) *task {
	now := time.Now().UTC()
	nextTask := &task{
		ID:          taskID,
		SessionID:   sessionID,
		Locator:     locator,
		ReportTo:    reportTo,
		Prompt:      strings.TrimSpace(prompt),
		Status:      taskStatusQueued,
		CreatedAt:   now,
		ScheduledAt: now,
	}
	c.tasks[taskID] = nextTask
	return nextTask
}

func (c *coordinator) enqueueQueuedTaskLocked(nextTask *task) error {
	c.logTaskEvent("task enqueued", nextTask)
	if c.isLocalAgentTarget(nextTask.Locator) {
		queue, ok := c.queues[nextTask.Locator.ID]
		if !ok {
			return fmt.Errorf("unknown local agent locator.id %q", nextTask.Locator.ID)
		}
		queue <- nextTask
		return nil
	}
	c.dispatchQueue <- nextTask
	return nil
}

func (c *coordinator) isLocalAgentTarget(locator Locator) bool {
	return locator.Type == LocatorTypeAgent && locator.Kind == LocatorKindLocal && locator.ID == c.rootAgentID || c.isLocalChildTarget(locator)
}

func (c *coordinator) isLocalChildTarget(locator Locator) bool {
	if locator.Type != LocatorTypeAgent || locator.Kind != LocatorKindLocal {
		return false
	}
	_, ok := c.childAgentIDs[locator.ID]
	return ok
}

func (c *coordinator) runDispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case nextTask := <-c.dispatchQueue:
			if nextTask == nil {
				continue
			}
			if !c.tryStartTask(nextTask) {
				continue
			}
			if err := c.providers.dispatchTask(ctx, DispatchRequest{
				TaskID:    nextTask.ID,
				SessionID: nextTask.SessionID,
				Locator:   nextTask.Locator,
				ReportTo:  nextTask.ReportTo,
				Prompt:    nextTask.Prompt,
			}); err != nil {
				c.handleTaskResult(nextTask, "", err)
				continue
			}
			c.handleDispatchHandoff(nextTask)
		}
	}
}

func (c *coordinator) handleDispatchHandoff(dispatchedTask *task) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	dispatchedTask.FinishedAt = now
	dispatchedTask.Status = taskStatusDispatched
	c.logTaskEvent("task dispatched", dispatchedTask)
}

func (c *coordinator) tryStartTask(nextTask *task) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		c.logTaskEvent("task skipped after terminal", nextTask)
		return false
	}
	now := time.Now().UTC()
	nextTask.ClaimedAt = now
	nextTask.StartedAt = now
	nextTask.Status = taskStatusRunning
	c.logTaskEvent("task started", nextTask)
	return true
}

func (c *coordinator) handleTaskResult(doneTask *task, output string, err error) {
	c.mu.Lock()
	now := time.Now().UTC()
	doneTask.FinishedAt = now
	doneTask.Output = strings.TrimSpace(output)
	var notification *task
	var reportRequest *ReportRequest
	if err != nil {
		if c.shuttingDown && errors.Is(err, context.Canceled) {
			doneTask.Status = taskStatusFailed
			doneTask.Error = err.Error()
			c.logTaskEvent("task canceled during shutdown", doneTask)
			c.mu.Unlock()
			return
		}
		doneTask.Status = taskStatusFailed
		doneTask.Error = err.Error()
		c.logTaskEvent("task failed", doneTask)
	} else {
		doneTask.Status = taskStatusCompleted
		c.logTaskEvent("task completed", doneTask)
	}

	if doneTask.Locator.ID == c.rootAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("%s task %q failed: %w", c.rootAgentID, doneTask.ID, err)
			c.terminal = true
			c.sendDoneLocked(RunResult{})
			c.mu.Unlock()
			return
		}
		c.latestRootOutput = doneTask.Output
		if c.terminal {
			c.sendDoneLocked(RunResult{Summary: c.finalSummary})
		}
		c.mu.Unlock()
		return
	}

	if !c.terminal {
		if c.isLocalAgentTarget(doneTask.ReportTo) {
			notificationPrompt := c.completionPromptFormatter(completionPromptInputFromTask(doneTask))
			notification = c.newQueuedTaskLocked(
				doneTask.ID+".notify",
				doneTask.SessionID,
				doneTask.ReportTo,
				NewAgentLocator(c.rootAgentID),
				notificationPrompt,
			)
			notification.SourceTaskID = doneTask.ID
			sourceLocator := doneTask.Locator
			notification.SourceLocator = &sourceLocator
			c.logTaskEvent("notification task created", notification)
		} else {
			reportRequest = buildReportRequest(doneTask, c.completionPromptFormatter(completionPromptInputFromTask(doneTask)))
		}
	}
	c.mu.Unlock()

	if notification != nil {
		if err := c.enqueueNotification(notification); err != nil {
			c.setRunError(fmt.Errorf("enqueue notification for %q: %w", doneTask.ID, err))
		}
	}
	if reportRequest != nil {
		if err := c.providers.deliverReport(context.Background(), *reportRequest); err != nil {
			c.setRunError(fmt.Errorf("deliver report for %q: %w", doneTask.ID, err))
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
		return fmt.Errorf("unknown local agent locator.id %q", nextTask.Locator.ID)
	}
	c.logTaskEvent("task enqueued", nextTask)
	queue <- nextTask
	return nil
}

func (c *coordinator) logTaskEvent(message string, nextTask *task) {
	event := c.logger.Debug().
		Str("task_id", nextTask.ID).
		Str("session_id", nextTask.SessionID).
		Interface("locator", nextTask.Locator).
		Interface("report_to", nextTask.ReportTo).
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

func (c *coordinator) sendDoneLocked(result RunResult) {
	if c.doneClosed {
		return
	}
	c.done <- result
	c.doneClosed = true
}

func completionPromptInputFromTask(doneTask *task) CompletionPromptInput {
	input := CompletionPromptInput{
		TaskID:    doneTask.ID,
		SessionID: doneTask.SessionID,
		Source:    doneTask.Locator,
		Status:    string(doneTask.Status),
		Output:    doneTask.Output,
		Error:     doneTask.Error,
	}
	if doneTask.SourceTaskID != "" {
		input.SourceTaskID = doneTask.SourceTaskID
	}
	if doneTask.SourceLocator != nil {
		input.Source = *doneTask.SourceLocator
	}
	return input
}

func buildReportRequest(doneTask *task, prompt string) *ReportRequest {
	return &ReportRequest{
		TaskID:        doneTask.ID,
		SessionID:     doneTask.SessionID,
		SourceTaskID:  doneTask.SourceTaskID,
		SourceLocator: doneTask.Locator,
		ReportTo:      doneTask.ReportTo,
		Status:        string(doneTask.Status),
		Prompt:        strings.TrimSpace(prompt),
		Output:        doneTask.Output,
		Error:         doneTask.Error,
	}
}

func formatElapsed(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func writeRunOutput(stdout io.Writer, summary string, elapsed string) error {
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		if _, err := fmt.Fprintln(stdout, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "Total run time: %s\n", elapsed)
	return err
}

func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func newACPSession(ctx context.Context, cfg acpSessionConfig) (*acpSession, error) {
	logger := cfg.Logger.With().Str("agent_id", cfg.AgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              cfg.Name,
		Description:       cfg.Description,
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       cfg.Instruction,
		MCPServers:        cfg.MCPServers,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}

	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        cfg.AppName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s runner: %w", cfg.AgentID, err)
	}
	return &acpSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        cfg.AppName,
		userID:         cfg.UserID,
		logger:         logger,
		sessions:       make(map[string]string),
	}, nil
}

func (s *acpSession) RunTask(ctx context.Context, sessionID string, callID string, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resolvedSessionID, err := s.ensureSessionLocked(ctx, sessionID)
	if err != nil {
		return "", err
	}
	callLogger := s.logger.With().Str("call_id", callID).Logger()
	_, last, err := runWithRunner(ctx, s.runner, s.sessionService, s.appName, s.userID, resolvedSessionID, prompt, func(output string) {
		callLogger.Debug().Str("output", output).Msg("task output")
	})
	return last, err
}

func (s *acpSession) ensureSessionLocked(ctx context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("session_id is required")
	}
	if resolved, ok := s.sessions[sessionID]; ok {
		return resolved, nil
	}
	if _, err := s.sessionService.Get(ctx, &session.GetRequest{
		AppName:   s.appName,
		UserID:    s.userID,
		SessionID: sessionID,
	}); err == nil {
		s.sessions[sessionID] = sessionID
		return sessionID, nil
	}
	created, err := s.sessionService.Create(ctx, &session.CreateRequest{
		AppName:   s.appName,
		UserID:    s.userID,
		SessionID: sessionID,
	})
	if err != nil {
		return "", err
	}
	s.sessions[sessionID] = created.Session.ID()
	return created.Session.ID(), nil
}

func (s *acpSession) Close() error {
	if s.agent == nil {
		return nil
	}
	return s.agent.Close()
}

func newChildSessions(ctx context.Context, cfg childSessionSetConfig) (runnerSet, error) {
	agents := make(map[string]closableRunner, len(cfg.ChildAgents))
	childIDs := make([]string, 0, len(cfg.ChildAgents))
	for agentID := range cfg.ChildAgents {
		childIDs = append(childIDs, agentID)
	}
	sort.Strings(childIDs)
	for _, agentID := range childIDs {
		child := cfg.ChildAgents[agentID]
		session, err := newACPSession(ctx, acpSessionConfig{
			AgentID:     agentID,
			AppName:     "taskmaster-" + agentID,
			Name:        child.Name,
			Description: child.Description,
			Instruction: child.Instruction,
			Command:     cfg.Command,
			WorkingDir:  cfg.WorkingDir,
			Stderr:      cfg.Stderr,
			Logger:      cfg.Logger,
			UserID:      cfg.RootAgentID,
		})
		if err != nil {
			for _, created := range agents {
				_ = created.Close()
			}
			return nil, err
		}
		agents[agentID] = session
	}
	return &childSessionSet{agents: agents}, nil
}

func (s *childSessionSet) Runner(agentID string) childInvoker {
	return s.agents[agentID]
}

func (s *childSessionSet) Close() error {
	var errs []string
	for _, runner := range s.agents {
		if err := runner.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
