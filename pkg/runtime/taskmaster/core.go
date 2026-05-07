package taskmaster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const defaultQueueDepth = 32

type AgentConfig struct {
	Name        string
	Description string
	Instruction string
}

type Task struct {
	ID        string   `json:"task_id"`
	SessionID string   `json:"session_id"`
	Locator   Locator  `json:"locator"`
	ReportTo  *Locator `json:"report_to,omitempty"`
	Content   string   `json:"content"`
}

type LocalRunner interface {
	RunTask(ctx context.Context, task Task) (string, error)
	Close() error
}

type Config struct {
	Logger       *zerolog.Logger
	RootAgentID  string
	LocalRunners map[string]LocalRunner
	Targets      []Target
}

type taskStatus string

const (
	taskStatusQueued     taskStatus = "queued"
	taskStatusRunning    taskStatus = "running"
	taskStatusCompleted  taskStatus = "completed"
	taskStatusDispatched taskStatus = "dispatched"
	taskStatusFailed     taskStatus = "failed"
)

type taskState struct {
	task       Task
	status     taskStatus
	output     string
	errText    string
	startedAt  time.Time
	finishedAt time.Time
}

type executor struct {
	agentID     string
	queue       <-chan *taskState
	runner      LocalRunner
	coordinator *coordinator
	logger      zerolog.Logger
}

type coordinator struct {
	logger zerolog.Logger

	rootAgentID     string
	defaultReportTo Locator
	localRunnerIDs  map[string]struct{}
	targets         targetRegistry
	requestStop     func()

	mu            sync.Mutex
	tasks         map[string]*taskState
	queues        map[string]chan *taskState
	dispatchQueue chan *taskState
	shuttingDown  bool
	finalErr      error

	wg sync.WaitGroup
}

type Runtime struct {
	logger zerolog.Logger

	rootAgentID string
	coordinator *coordinator

	localIDs     []string
	localRunners map[string]LocalRunner

	startMu  sync.Mutex
	started  bool
	stopOnce sync.Once
	done     chan struct{}

	runCtx context.Context
	cancel context.CancelFunc

	errMu sync.Mutex
	err   error
}

func New(cfg Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		logger := zerolog.Nop()
		baseLogger = &logger
	}
	logger := baseLogger.With().Logger()
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))

	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		return nil, err
	}

	localRunners := make(map[string]LocalRunner, len(cfg.LocalRunners))
	localIDs := make([]string, 0, len(cfg.LocalRunners))
	for id, runner := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(id))
		localRunners[normalizedID] = runner
		localIDs = append(localIDs, normalizedID)
	}

	runtime := &Runtime{
		logger:       logger,
		rootAgentID:  rootID,
		coordinator:  coordinator,
		localIDs:     localIDs,
		localRunners: localRunners,
		done:         make(chan struct{}),
	}
	coordinator.requestStop = runtime.requestStop
	return runtime, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	r.startMu.Lock()
	defer r.startMu.Unlock()
	if r.started {
		return errors.New("runtime already started")
	}
	r.started = true
	r.runCtx, r.cancel = context.WithCancel(ctx)
	r.coordinator.start(r.runCtx, r.localRunners)
	go r.shutdownOnContextDone()
	return nil
}

func (r *Runtime) Enqueue(task Task) error {
	r.startMu.Lock()
	started := r.started
	r.startMu.Unlock()
	if !started {
		return errors.New("runtime is not started")
	}
	return r.coordinator.enqueue(task)
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.startMu.Lock()
	started := r.started
	cancel := r.cancel
	done := r.done
	r.startMu.Unlock()
	if !started {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
		return r.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Done() <-chan struct{} {
	return r.done
}

func (r *Runtime) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}

func (r *Runtime) requestStop() {
	r.startMu.Lock()
	cancel := r.cancel
	r.startMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Runtime) shutdownOnContextDone() {
	<-r.runCtx.Done()
	r.stopOnce.Do(func() {
		r.coordinator.beginShutdown()
		r.coordinator.wait()
		r.setErr(r.coordinator.runtimeErr())
		closeErr := r.closeRunners()
		if closeErr != nil && r.Err() == nil {
			r.setErr(closeErr)
		}
		close(r.done)
	})
}

func (r *Runtime) closeRunners() error {
	var errs []string
	for _, id := range r.localIDs {
		runner := r.localRunners[id]
		if runner == nil {
			continue
		}
		if err := runner.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) setErr(err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.RootAgentID) == "" {
		return errors.New("root agent id is required")
	}
	if len(cfg.LocalRunners) == 0 {
		return errors.New("at least one local runner is required")
	}

	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))
	hasRoot := false
	for runnerID, runner := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(runnerID))
		if normalizedID == "" {
			return errors.New("local runner id is required")
		}
		if runner == nil {
			return fmt.Errorf("local runner %q is nil", runnerID)
		}
		if normalizedID == rootID {
			hasRoot = true
		}
	}
	if !hasRoot {
		return fmt.Errorf("root local runner %q is missing", cfg.RootAgentID)
	}
	return nil
}

func newCoordinator(logger zerolog.Logger, cfg Config) (*coordinator, error) {
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))
	localRunnerIDs := make(map[string]struct{}, len(cfg.LocalRunners))
	queues := make(map[string]chan *taskState, len(cfg.LocalRunners))
	for runnerID := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(runnerID))
		localRunnerIDs[normalizedID] = struct{}{}
		queues[normalizedID] = make(chan *taskState, defaultQueueDepth)
	}

	return &coordinator{
		logger:          logger,
		rootAgentID:     rootID,
		defaultReportTo: NewAgentLocator(rootID),
		localRunnerIDs:  localRunnerIDs,
		targets:         newTargetRegistry(cfg.Targets),
		tasks:           make(map[string]*taskState),
		queues:          queues,
		dispatchQueue:   make(chan *taskState, defaultQueueDepth),
	}, nil
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
			if !e.coordinator.tryStartTask(nextTask) {
				continue
			}
			e.logger.Info().
				Str("agent_id", e.agentID).
				Str("task_id", nextTask.task.ID).
				Str("session_id", nextTask.task.SessionID).
				Str("task", nextTask.task.Content).
				Msg("agent received task")
			output, err := e.runner.RunTask(ctx, nextTask.task)
			if err != nil {
				event := e.logger.Info().
					Str("agent_id", e.agentID).
					Str("task_id", nextTask.task.ID).
					Str("session_id", nextTask.task.SessionID).
					Str("error", err.Error())
				if e.coordinator.isExpectedShutdownCancel(err) {
					event = e.logger.Debug().
						Str("agent_id", e.agentID).
						Str("task_id", nextTask.task.ID).
						Str("session_id", nextTask.task.SessionID).
						Str("error", err.Error())
					event.Msg("agent task canceled during shutdown")
				} else {
					event.Msg("agent finished task")
				}
			} else {
				e.logger.Info().
					Str("agent_id", e.agentID).
					Str("task_id", nextTask.task.ID).
					Str("session_id", nextTask.task.SessionID).
					Str("result", strings.TrimSpace(output)).
					Msg("agent finished task")
			}
			e.coordinator.handleTaskResult(nextTask, output, err)
		}
	}
}

func (c *coordinator) start(ctx context.Context, runners map[string]LocalRunner) {
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

func (c *coordinator) enqueue(task Task) error {
	taskID := strings.TrimSpace(task.ID)
	sessionID := strings.TrimSpace(task.SessionID)
	content := strings.TrimSpace(task.Content)
	locator, err := NormalizeLocator(task.Locator)
	if err != nil {
		return err
	}
	reportTo, err := normalizeReportLocator(task.ReportTo, c.defaultReportTo)
	if err != nil {
		return err
	}
	if taskID == "" {
		return errors.New("task_id is required")
	}
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	if content == "" {
		return errors.New("content is required")
	}
	if isBuiltInSourceLocator(locator) {
		return fmt.Errorf("source locator %s cannot be used as a target", locator)
	}
	if err := c.validateReportTo(reportTo); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		return errors.New("runtime is stopping")
	}
	if _, exists := c.tasks[taskID]; exists {
		return fmt.Errorf("task %q already exists", taskID)
	}

	queued := c.newQueuedTaskLocked(Task{
		ID:        taskID,
		SessionID: sessionID,
		Locator:   locator,
		ReportTo:  &reportTo,
		Content:   content,
	})
	return c.enqueueQueuedTaskLocked(queued)
}

func (c *coordinator) validateReportTo(reportTo Locator) error {
	if isBuiltInSourceLocator(reportTo) {
		return fmt.Errorf("source locator %s cannot be used as report_to", reportTo)
	}
	if c.isLocalAgentTarget(reportTo) {
		return nil
	}
	if c.targets.supports(reportTo) {
		return nil
	}
	return fmt.Errorf("unsupported report_to locator %s", reportTo)
}

func (c *coordinator) beginShutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shuttingDown = true
}

func (c *coordinator) runtimeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalErr
}

func (c *coordinator) setFinalErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalErr != nil {
		return
	}
	c.finalErr = err
	c.shuttingDown = true
}

func (c *coordinator) newQueuedTaskLocked(task Task) *taskState {
	nextTask := &taskState{
		task:   task,
		status: taskStatusQueued,
	}
	c.tasks[task.ID] = nextTask
	return nextTask
}

func (c *coordinator) enqueueQueuedTaskLocked(nextTask *taskState) error {
	c.logTaskEvent("task enqueued", nextTask)
	if c.isLocalAgentTarget(nextTask.task.Locator) {
		queue, ok := c.queues[nextTask.task.Locator.Key]
		if !ok {
			return fmt.Errorf("unknown local agent locator.key %q", nextTask.task.Locator.Key)
		}
		queue <- nextTask
		return nil
	}
	c.dispatchQueue <- nextTask
	return nil
}

func (c *coordinator) isLocalAgentTarget(locator Locator) bool {
	if locator.Class != LocatorClassAgent || locator.Transport != LocatorTransportLocal {
		return false
	}
	_, ok := c.localRunnerIDs[locator.Key]
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
			if err := c.targets.dispatchTask(ctx, nextTask.task); err != nil {
				c.handleTaskResult(nextTask, "", err)
				continue
			}
			c.handleDispatchHandoff(nextTask)
		}
	}
}

func (c *coordinator) handleDispatchHandoff(dispatchedTask *taskState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dispatchedTask.finishedAt = time.Now().UTC()
	dispatchedTask.status = taskStatusDispatched
	c.logTaskEvent("task dispatched", dispatchedTask)
}

func (c *coordinator) tryStartTask(nextTask *taskState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		c.logTaskEvent("task skipped during shutdown", nextTask)
		return false
	}
	now := time.Now().UTC()
	nextTask.startedAt = now
	nextTask.status = taskStatusRunning
	c.logTaskEvent("task started", nextTask)
	return true
}

func (c *coordinator) handleTaskResult(doneTask *taskState, output string, err error) {
	c.mu.Lock()
	doneTask.finishedAt = time.Now().UTC()
	doneTask.output = strings.TrimSpace(output)
	if err != nil {
		if c.shuttingDown && errors.Is(err, context.Canceled) {
			doneTask.status = taskStatusFailed
			doneTask.errText = err.Error()
			c.logTaskEvent("task canceled during shutdown", doneTask)
			c.mu.Unlock()
			return
		}
		doneTask.status = taskStatusFailed
		doneTask.errText = err.Error()
		c.logTaskEvent("task failed", doneTask)
	} else {
		doneTask.status = taskStatusCompleted
		c.logTaskEvent("task completed", doneTask)
	}

	if doneTask.task.Locator.Key == c.rootAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("%s task %q failed: %w", c.rootAgentID, doneTask.task.ID, err)
			c.shuttingDown = true
			stop := c.requestStop
			c.mu.Unlock()
			if stop != nil {
				stop()
			}
			return
		}
		c.mu.Unlock()
		return
	}

	if c.shuttingDown {
		c.mu.Unlock()
		return
	}

	reportTo := c.defaultReportTo
	if doneTask.task.ReportTo != nil {
		reportTo = *doneTask.task.ReportTo
	}
	notification := Task{
		ID:        doneTask.task.ID + ".notify",
		SessionID: doneTask.task.SessionID,
		Locator:   reportTo,
		Content:   formatReportTask(doneTask.task.ID, doneTask.task.SessionID, doneTask.task.Locator, doneTask.output, err),
	}
	nextTask := c.newQueuedTaskLocked(notification)
	c.logTaskEvent("notification task created", nextTask)
	c.mu.Unlock()

	if err := c.enqueueNotification(nextTask); err != nil {
		c.setFinalErr(fmt.Errorf("enqueue notification for %q: %w", doneTask.task.ID, err))
		if c.requestStop != nil {
			c.requestStop()
		}
	}
}

func (c *coordinator) enqueueNotification(nextTask *taskState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		return errors.New("runtime is stopping")
	}
	return c.enqueueQueuedTaskLocked(nextTask)
}

func (c *coordinator) logTaskEvent(message string, nextTask *taskState) {
	event := c.logger.Debug().
		Str("task_id", nextTask.task.ID).
		Str("session_id", nextTask.task.SessionID).
		Str("locator", nextTask.task.Locator.String()).
		Str("status", string(nextTask.status))
	if nextTask.task.ReportTo != nil {
		event = event.Str("report_to", nextTask.task.ReportTo.String())
	}
	if nextTask.output != "" {
		event = event.Str("output", nextTask.output)
	}
	if nextTask.errText != "" {
		event = event.Str("error", nextTask.errText)
	}
	event.Msg(message)
}

func (c *coordinator) isExpectedShutdownCancel(err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shuttingDown
}

func formatReportTask(taskID string, sessionID string, source Locator, output string, err error) string {
	lines := []string{
		"Session ID:",
		strings.TrimSpace(sessionID),
		"",
		"Source:",
		strings.Join([]string{source.Class, source.Transport, source.Key}, "/"),
		"",
	}
	if err != nil {
		lines = append(lines,
			"Task "+strings.TrimSpace(taskID)+" failed.",
			"",
			"Error:",
			strings.TrimSpace(err.Error()),
		)
		return strings.Join(lines, "\n")
	}
	result := strings.TrimSpace(output)
	if result == "" {
		result = "(empty result)"
	}
	lines = append(lines,
		"Task "+strings.TrimSpace(taskID)+" completed.",
		"",
		"Result:",
		result,
	)
	return strings.Join(lines, "\n")
}
