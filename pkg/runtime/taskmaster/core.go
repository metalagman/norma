package taskmaster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
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
	ID            string   `json:"task_id"`
	SessionID     string   `json:"session_id"`
	Locator       Locator  `json:"locator"`
	ReportTo      *Locator `json:"report_to,omitempty"`
	Content       string   `json:"content"`
	SourceTaskID  string   `json:"source_task_id,omitempty"`
	SourceLocator *Locator `json:"source_locator,omitempty"`
}

type RunResult struct {
	Summary string
	Stopped bool
}

type ShutdownInput struct {
	Summary string
	Cause   error
}

type ShutdownSummaryInput struct {
	LastRootOutput string
	Err            error
}

type LocalRunner interface {
	RunTask(ctx context.Context, task Task) (string, error)
	Close() error
}

type Config struct {
	Logger                     *zerolog.Logger
	RootAgentID                string
	LocalRunners               map[string]LocalRunner
	DefaultReportTo            Locator
	Targets                    []Target
	ReportTaskContentFormatter func(source Task, output string, err error) string
	ShutdownSummaryFormatter   func(ShutdownSummaryInput) string
	Closers                    []io.Closer
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
	task Task

	Status      taskStatus
	CreatedAt   time.Time
	ScheduledAt time.Time
	ClaimedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	Output      string
	Error       string
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

	rootAgentID                string
	localRunnerIDs             map[string]struct{}
	defaultReportTo            Locator
	reportTaskContentFormatter func(source Task, output string, err error) string
	shutdownSummaryFormatter   func(ShutdownSummaryInput) string
	targets                    targetRegistry

	mu               sync.Mutex
	tasks            map[string]*taskState
	queues           map[string]chan *taskState
	dispatchQueue    chan *taskState
	terminal         bool
	shuttingDown     bool
	finalSummary     string
	latestRootOutput string
	finalErr         error
	done             chan RunResult
	doneClosed       bool

	wg sync.WaitGroup
}

type Runtime struct {
	logger zerolog.Logger

	runCtx       context.Context
	cancel       context.CancelFunc
	coordinator  *coordinator
	localIDs     []string
	localRunners map[string]LocalRunner
	closers      []io.Closer

	shutdownMu sync.Mutex
	shutdown   bool
	closeOnce  sync.Once
	closeErr   error
}

func Start(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		baseLogger = zerolog.Ctx(ctx)
	}
	logger := baseLogger.With().Logger()

	runCtx, cancel := context.WithCancel(ctx)
	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	localRunners := make(map[string]LocalRunner, len(cfg.LocalRunners))
	localIDs := make([]string, 0, len(cfg.LocalRunners))
	for id, runner := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(id))
		localRunners[normalizedID] = runner
		localIDs = append(localIDs, normalizedID)
	}
	sort.Strings(localIDs)
	coordinator.start(runCtx, localRunners)

	return &Runtime{
		logger:       logger,
		runCtx:       runCtx,
		cancel:       cancel,
		coordinator:  coordinator,
		localIDs:     localIDs,
		localRunners: localRunners,
		closers:      append([]io.Closer(nil), cfg.Closers...),
	}, nil
}

func (r *Runtime) Enqueue(task Task) error {
	return r.coordinator.enqueue(task)
}

func (r *Runtime) Finish(summary string) error {
	return r.coordinator.finish(summary)
}

func (r *Runtime) Wait() (RunResult, error) {
	return r.coordinator.waitResult()
}

func (r *Runtime) Shutdown(ctx context.Context, input ShutdownInput) (RunResult, error) {
	r.shutdownMu.Lock()
	if r.shutdown {
		r.shutdownMu.Unlock()
		if err := r.Close(); err != nil {
			return RunResult{}, err
		}
		return r.coordinator.shutdownResult(input), r.coordinator.runtimeErr()
	}
	r.shutdown = true
	r.shutdownMu.Unlock()

	r.coordinator.beginShutdown()
	r.cancel()

	waitDone := make(chan struct{})
	go func() {
		r.coordinator.wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}

	if err := r.closeResources(); err != nil {
		return RunResult{}, err
	}

	if err := r.coordinator.runtimeErr(); err != nil {
		return RunResult{}, err
	}
	return r.coordinator.shutdownResult(input), nil
}

func (r *Runtime) Close() error {
	r.coordinator.beginShutdown()
	r.cancel()
	r.coordinator.wait()
	return r.closeResources()
}

func (r *Runtime) closeResources() error {
	r.closeOnce.Do(func() {
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
		for _, closer := range r.closers {
			if closer == nil {
				continue
			}
			if err := closer.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			r.closeErr = errors.New(strings.Join(errs, "; "))
		}
	})
	return r.closeErr
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.RootAgentID) == "" {
		return errors.New("root agent id is required")
	}
	if len(cfg.LocalRunners) == 0 {
		return errors.New("at least one local runner is required")
	}

	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))
	defaultReportTo, err := NormalizeLocator(cfg.DefaultReportTo)
	if err != nil {
		return fmt.Errorf("default report_to: %w", err)
	}
	if defaultReportTo.Class != LocatorClassAgent || defaultReportTo.Transport != LocatorTransportLocal {
		return errors.New("default report_to must be the local root agent locator")
	}
	if defaultReportTo.Key != rootID {
		return fmt.Errorf("default report_to.key %q must match root agent id %q", defaultReportTo.Key, cfg.RootAgentID)
	}

	normalizedIDs := make(map[string]struct{}, len(cfg.LocalRunners))
	for runnerID, runner := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(runnerID))
		if normalizedID == "" {
			return errors.New("local runner id is required")
		}
		if runner == nil {
			return fmt.Errorf("local runner %q is nil", runnerID)
		}
		normalizedIDs[normalizedID] = struct{}{}
	}
	if _, ok := normalizedIDs[rootID]; !ok {
		return fmt.Errorf("root local runner %q is missing", cfg.RootAgentID)
	}
	if cfg.ReportTaskContentFormatter == nil {
		return errors.New("report task content formatter is required")
	}
	return nil
}

func newCoordinator(logger zerolog.Logger, cfg Config) (*coordinator, error) {
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootAgentID))
	defaultReportTo, err := NormalizeLocator(cfg.DefaultReportTo)
	if err != nil {
		return nil, fmt.Errorf("default report_to: %w", err)
	}

	localRunnerIDs := make(map[string]struct{}, len(cfg.LocalRunners))
	queues := make(map[string]chan *taskState, len(cfg.LocalRunners))
	for runnerID := range cfg.LocalRunners {
		normalizedID := strings.ToLower(strings.TrimSpace(runnerID))
		localRunnerIDs[normalizedID] = struct{}{}
		queues[normalizedID] = make(chan *taskState, defaultQueueDepth)
	}

	return &coordinator{
		logger:                     logger,
		rootAgentID:                rootID,
		localRunnerIDs:             localRunnerIDs,
		defaultReportTo:            defaultReportTo,
		reportTaskContentFormatter: cfg.ReportTaskContentFormatter,
		shutdownSummaryFormatter:   cfg.ShutdownSummaryFormatter,
		targets:                    newTargetRegistry(cfg.Targets),
		tasks:                      make(map[string]*taskState),
		queues:                     queues,
		dispatchQueue:              make(chan *taskState, defaultQueueDepth),
		done:                       make(chan RunResult, 1),
	}, nil
}

func (e *executor) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case nextTask := <-e.queue:
			if nextTask == nil {
				continue
			}
			if nextTask.task.SourceTaskID != "" {
				e.logger.Debug().
					Str("task_id", nextTask.task.ID).
					Str("source_task_id", nextTask.task.SourceTaskID).
					Str("source_locator", locatorPtrString(nextTask.task.SourceLocator)).
					Str("report_to", locatorPtrString(nextTask.task.ReportTo)).
					Msg("notification task received")
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

	var sourceLocator *Locator
	if task.SourceLocator != nil {
		normalizedSource, err := NormalizeLocator(*task.SourceLocator)
		if err != nil {
			return fmt.Errorf("source_locator: %w", err)
		}
		sourceLocator = &normalizedSource
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal || c.shuttingDown {
		return errors.New("run already finished")
	}
	if _, exists := c.tasks[taskID]; exists {
		return fmt.Errorf("task %q already exists", taskID)
	}

	queued := c.newQueuedTaskLocked(Task{
		ID:            taskID,
		SessionID:     sessionID,
		Locator:       locator,
		ReportTo:      &reportTo,
		Content:       content,
		SourceTaskID:  strings.TrimSpace(task.SourceTaskID),
		SourceLocator: sourceLocator,
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

func (c *coordinator) beginShutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shuttingDown = true
}

func (c *coordinator) waitResult() (RunResult, error) {
	result := <-c.done
	if err := c.runtimeErr(); err != nil {
		return RunResult{}, err
	}
	return result, nil
}

func (c *coordinator) shutdownResult(input ShutdownInput) RunResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	summary := strings.TrimSpace(input.Summary)
	if summary == "" && c.shutdownSummaryFormatter != nil {
		summary = strings.TrimSpace(c.shutdownSummaryFormatter(ShutdownSummaryInput{
			LastRootOutput: strings.TrimSpace(c.latestRootOutput),
			Err:            input.Cause,
		}))
	}
	return RunResult{
		Summary: summary,
		Stopped: true,
	}
}

func (c *coordinator) runtimeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalErr
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

func (c *coordinator) newQueuedTaskLocked(task Task) *taskState {
	now := time.Now().UTC()
	nextTask := &taskState{
		task:        task,
		Status:      taskStatusQueued,
		CreatedAt:   now,
		ScheduledAt: now,
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
		if ctx.Err() != nil {
			return
		}
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
	dispatchedTask.FinishedAt = time.Now().UTC()
	dispatchedTask.Status = taskStatusDispatched
	c.logTaskEvent("task dispatched", dispatchedTask)
}

func (c *coordinator) tryStartTask(nextTask *taskState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		c.logTaskEvent("task skipped after terminal", nextTask)
		return false
	}
	if c.shuttingDown {
		c.logTaskEvent("task skipped during shutdown", nextTask)
		return false
	}
	now := time.Now().UTC()
	nextTask.ClaimedAt = now
	nextTask.StartedAt = now
	nextTask.Status = taskStatusRunning
	c.logTaskEvent("task started", nextTask)
	return true
}

func (c *coordinator) handleTaskResult(doneTask *taskState, output string, err error) {
	c.mu.Lock()
	doneTask.FinishedAt = time.Now().UTC()
	doneTask.Output = strings.TrimSpace(output)
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

	if doneTask.task.Locator.Key == c.rootAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("%s task %q failed: %w", c.rootAgentID, doneTask.task.ID, err)
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

	if c.terminal {
		c.mu.Unlock()
		return
	}

	reportTo := c.defaultReportTo
	if doneTask.task.ReportTo != nil {
		reportTo = *doneTask.task.ReportTo
	}
	notification := Task{
		ID:           doneTask.task.ID + ".notify",
		SessionID:    doneTask.task.SessionID,
		Locator:      reportTo,
		Content:      c.reportTaskContentFormatter(doneTask.task, doneTask.Output, err),
		SourceTaskID: doneTask.task.ID,
	}
	sourceLocator := doneTask.task.Locator
	notification.SourceLocator = &sourceLocator
	nextTask := c.newQueuedTaskLocked(notification)
	c.logTaskEvent("notification task created", nextTask)
	c.mu.Unlock()

	if err := c.enqueueNotification(nextTask); err != nil {
		c.setRunError(fmt.Errorf("enqueue notification for %q: %w", doneTask.task.ID, err))
	}
}

func (c *coordinator) enqueueNotification(nextTask *taskState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal || c.shuttingDown {
		return errors.New("run already finished")
	}
	return c.enqueueQueuedTaskLocked(nextTask)
}

func (c *coordinator) logTaskEvent(message string, nextTask *taskState) {
	event := c.logger.Debug().
		Str("task_id", nextTask.task.ID).
		Str("session_id", nextTask.task.SessionID).
		Str("locator", nextTask.task.Locator.String()).
		Str("status", string(nextTask.Status))
	if nextTask.task.ReportTo != nil {
		event = event.Str("report_to", nextTask.task.ReportTo.String())
	}
	if nextTask.task.SourceTaskID != "" {
		event = event.Str("source_task_id", nextTask.task.SourceTaskID)
	}
	if nextTask.task.SourceLocator != nil {
		event = event.Str("source_locator", locatorPtrString(nextTask.task.SourceLocator))
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

func (c *coordinator) isExpectedShutdownCancel(err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shuttingDown
}

func FormatElapsed(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func WriteRunOutput(stdout io.Writer, summary string, elapsed string) error {
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		if _, err := fmt.Fprintln(stdout, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "Total run time: %s\n", elapsed)
	return err
}
