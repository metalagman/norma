package goalkeeper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

const (
	schedulerAgentID    = "scheduler"
	defaultQueueDepth   = 32
	initialGoalJobID    = "goal-job"
	defaultMaxAttempts  = 1
	notifySchedulerName = "GoalkeeperNotifyScheduler"
)

var pdcaRoleOrder = []string{"plan", "do", "check", "act"}

type notifyJobKind string

const (
	notifyJobKindAgent     notifyJobKind = "agent"
	notifyJobKindScheduler notifyJobKind = "scheduler"
)

type notifyJobStatus string

const (
	notifyJobStatusQueued    notifyJobStatus = "queued"
	notifyJobStatusRunning   notifyJobStatus = "running"
	notifyJobStatusCompleted notifyJobStatus = "completed"
	notifyJobStatusFailed    notifyJobStatus = "failed"
)

type notifyJob struct {
	ID            string
	Kind          notifyJobKind
	TargetAgentID string
	Input         string
	ReplyTo       string
	Status        notifyJobStatus
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

type notifyRunResult struct {
	Summary string
}

type notifyJobRunner interface {
	RunJob(ctx context.Context, jobID string, task string) (string, error)
}

type schedulerSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	sessionID      string
	logger         zerolog.Logger
}

type schedulerSessionConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Logger     zerolog.Logger
	MCPServers map[string]acpagent.MCPServerConfig
}

func newSchedulerSession(ctx context.Context, cfg schedulerSessionConfig) (*schedulerSession, error) {
	logger := cfg.Logger.With().Str("role", schedulerAgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              notifySchedulerName,
		Description:       "Goalkeeper async scheduler agent",
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       notifySchedulerInstruction(),
		MCPServers:        cfg.MCPServers,
	})
	if err != nil {
		return nil, fmt.Errorf("create scheduler agent: %w", err)
	}
	sessionService := session.InMemoryService()
	const appName = "goalkeeper-notify-scheduler"
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create scheduler runner: %w", err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    "goalkeeper",
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create scheduler session: %w", err)
	}
	return &schedulerSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        appName,
		sessionID:      created.Session.ID(),
		logger:         logger,
	}, nil
}

func (s *schedulerSession) RunJob(ctx context.Context, jobID string, task string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobLogger := s.logger.With().Str("job_id", jobID).Logger()
	_, last, err := runWithRunner(ctx, s.runner, s.sessionService, s.appName, "goalkeeper", s.sessionID, task, func(output string) {
		jobLogger.Debug().Str("output", output).Msg("job output")
	})
	return last, err
}

func (s *schedulerSession) close() {
	if s.agent != nil {
		_ = s.agent.Close()
	}
}

type notifyExecutor struct {
	agentID     string
	queue       <-chan *notifyJob
	runner      notifyJobRunner
	coordinator *notifyCoordinator
	logger      zerolog.Logger
}

func (e *notifyExecutor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-e.queue:
			if job == nil {
				continue
			}
			if job.Kind == notifyJobKindScheduler && job.ReplyTo != "" {
				e.logger.Debug().
					Str("job_id", job.ID).
					Str("reply_to", job.ReplyTo).
					Msg("notification job received")
			}
			e.coordinator.markJobStarted(job)
			output, err := e.runner.RunJob(ctx, job.ID, job.Input)
			e.coordinator.handleJobResult(job, output, err)
		}
	}
}

type notifyCoordinator struct {
	logger zerolog.Logger

	mu               sync.Mutex
	jobs             map[string]*notifyJob
	queues           map[string]chan *notifyJob
	expectedRoleIdx  int
	workerInFlightID string
	terminal         bool
	finalSummary     string
	finalErr         error
	done             chan notifyRunResult
	doneClosed       bool

	wg sync.WaitGroup
}

func newNotifyCoordinator(logger zerolog.Logger, runners map[string]notifyJobRunner) (*notifyCoordinator, error) {
	for _, agentID := range append([]string{schedulerAgentID}, pdcaRoleOrder...) {
		if runners[agentID] == nil {
			return nil, fmt.Errorf("missing runner for agent %q", agentID)
		}
	}
	c := &notifyCoordinator{
		logger:          logger,
		jobs:            make(map[string]*notifyJob),
		queues:          make(map[string]chan *notifyJob),
		expectedRoleIdx: 0,
		done:            make(chan notifyRunResult, 1),
	}
	for agentID := range runners {
		c.queues[agentID] = make(chan *notifyJob, defaultQueueDepth)
	}
	return c, nil
}

func (c *notifyCoordinator) start(ctx context.Context, runners map[string]notifyJobRunner) {
	for agentID, runner := range runners {
		executor := &notifyExecutor{
			agentID:     agentID,
			queue:       c.queues[agentID],
			runner:      runner,
			coordinator: c,
			logger:      c.logger.With().Str("agent_id", agentID).Logger(),
		}
		c.wg.Add(1)
		go func(exec *notifyExecutor) {
			defer c.wg.Done()
			exec.run(ctx)
		}(executor)
	}
}

func (c *notifyCoordinator) wait() {
	c.wg.Wait()
}

func (c *notifyCoordinator) enqueueInitialGoal(goal string) error {
	job := &notifyJob{
		ID:            initialGoalJobID,
		Kind:          notifyJobKindScheduler,
		TargetAgentID: schedulerAgentID,
		Input:         "GOAL JOB:\n" + strings.TrimSpace(goal),
		Status:        notifyJobStatusQueued,
		Attempt:       1,
		MaxAttempts:   defaultMaxAttempts,
	}
	return c.enqueueJob(job)
}

func (c *notifyCoordinator) scheduleWorkerJob(jobID string, targetAgentID string, task string) error {
	jobID = strings.TrimSpace(jobID)
	targetAgentID = strings.ToLower(strings.TrimSpace(targetAgentID))
	task = strings.TrimSpace(task)
	if jobID == "" {
		return errors.New("job_id is required")
	}
	if task == "" {
		return errors.New("task is required")
	}
	if _, ok := pdcaRoles[targetAgentID]; !ok {
		return fmt.Errorf("unknown target_agent_id %q", targetAgentID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	if _, exists := c.jobs[jobID]; exists {
		return fmt.Errorf("job %q already exists", jobID)
	}
	if c.workerInFlightID != "" {
		return fmt.Errorf("job %q is still in flight; wait for notification", c.workerInFlightID)
	}
	if c.expectedRoleIdx >= len(pdcaRoleOrder) {
		return errors.New("pdca workflow is already complete")
	}
	expectedRole := pdcaRoleOrder[c.expectedRoleIdx]
	if targetAgentID != expectedRole {
		return fmt.Errorf("expected next target_agent_id %q, got %q", expectedRole, targetAgentID)
	}

	job := c.newQueuedJobLocked(jobID, notifyJobKindAgent, targetAgentID, task, schedulerAgentID)
	c.workerInFlightID = jobID
	return c.enqueueQueuedJobLocked(job)
}

func (c *notifyCoordinator) finish(summary string) error {
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

func (c *notifyCoordinator) waitResult(ctx context.Context) (notifyRunResult, error) {
	select {
	case <-ctx.Done():
		return notifyRunResult{}, ctx.Err()
	case result := <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finalErr != nil {
			return notifyRunResult{}, c.finalErr
		}
		return result, nil
	}
}

func (c *notifyCoordinator) setRunError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doneClosed {
		return
	}
	c.terminal = true
	c.finalErr = err
	c.sendDoneLocked(notifyRunResult{})
}

func (c *notifyCoordinator) enqueueJob(job *notifyJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	if _, exists := c.jobs[job.ID]; exists {
		return fmt.Errorf("job %q already exists", job.ID)
	}
	job = c.newQueuedJobLocked(job.ID, job.Kind, job.TargetAgentID, job.Input, job.ReplyTo)
	return c.enqueueQueuedJobLocked(job)
}

func (c *notifyCoordinator) newQueuedJobLocked(jobID string, kind notifyJobKind, targetAgentID string, input string, replyTo string) *notifyJob {
	now := time.Now().UTC()
	job := &notifyJob{
		ID:            jobID,
		Kind:          kind,
		TargetAgentID: targetAgentID,
		Input:         input,
		ReplyTo:       replyTo,
		Status:        notifyJobStatusQueued,
		Attempt:       1,
		MaxAttempts:   defaultMaxAttempts,
		CreatedAt:     now,
		ScheduledAt:   now,
	}
	c.jobs[jobID] = job
	return job
}

func (c *notifyCoordinator) enqueueQueuedJobLocked(job *notifyJob) error {
	queue, ok := c.queues[job.TargetAgentID]
	if !ok {
		return fmt.Errorf("unknown target_agent_id %q", job.TargetAgentID)
	}
	c.logJobEvent("job enqueued", job)
	queue <- job
	return nil
}

func (c *notifyCoordinator) markJobStarted(job *notifyJob) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	job.ClaimedAt = now
	job.StartedAt = now
	job.Status = notifyJobStatusRunning
	c.logJobEvent("job started", job)
}

func (c *notifyCoordinator) handleJobResult(job *notifyJob, output string, err error) {
	c.mu.Lock()
	now := time.Now().UTC()
	job.FinishedAt = now
	job.Output = strings.TrimSpace(output)
	var notification *notifyJob
	if err != nil {
		job.Status = notifyJobStatusFailed
		job.Error = err.Error()
		c.logJobEvent("job failed", job)
	} else {
		job.Status = notifyJobStatusCompleted
		c.logJobEvent("job completed", job)
	}

	if job.TargetAgentID == schedulerAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("scheduler job %q failed: %w", job.ID, err)
			c.terminal = true
			c.sendDoneLocked(notifyRunResult{})
			c.mu.Unlock()
			return
		}
		if c.terminal {
			c.sendDoneLocked(notifyRunResult{Summary: c.finalSummary})
		}
		c.mu.Unlock()
		return
	}

	if c.workerInFlightID == job.ID {
		c.workerInFlightID = ""
	}
	if err == nil && c.expectedRoleIdx < len(pdcaRoleOrder) && pdcaRoleOrder[c.expectedRoleIdx] == job.TargetAgentID {
		c.expectedRoleIdx++
	}
	if !c.terminal {
		notification = c.newQueuedJobLocked(
			job.ID+".notify",
			notifyJobKindScheduler,
			schedulerAgentID,
			formatNotificationJobInput(job),
			job.ID,
		)
		c.logJobEvent("notification job created", notification)
	}
	c.mu.Unlock()

	if notification != nil {
		if err := c.enqueueNotification(notification); err != nil {
			c.setRunError(fmt.Errorf("enqueue notification for %q: %w", job.ID, err))
		}
	}
}

func (c *notifyCoordinator) enqueueNotification(job *notifyJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal {
		return errors.New("run already finished")
	}
	queue, ok := c.queues[job.TargetAgentID]
	if !ok {
		return fmt.Errorf("unknown target_agent_id %q", job.TargetAgentID)
	}
	c.logJobEvent("job enqueued", job)
	queue <- job
	return nil
}

func (c *notifyCoordinator) logJobEvent(message string, job *notifyJob) {
	event := c.logger.Debug().
		Str("job_id", job.ID).
		Str("kind", string(job.Kind)).
		Str("target_agent_id", job.TargetAgentID).
		Str("status", string(job.Status))
	if job.ReplyTo != "" {
		event = event.Str("reply_to", job.ReplyTo)
	}
	if job.Output != "" {
		event = event.Str("output", job.Output)
	}
	if job.Error != "" {
		event = event.Str("error", job.Error)
	}
	event.Msg(message)
}

func (c *notifyCoordinator) sendDoneLocked(result notifyRunResult) {
	if c.doneClosed {
		return
	}
	c.done <- result
	c.doneClosed = true
}

func formatNotificationJobInput(job *notifyJob) string {
	result := job.Output
	if job.Error != "" {
		result = job.Error
	}
	return strings.TrimSpace(fmt.Sprintf(
		"JOB NOTIFICATION:\nsource_job_id: %s\nsource_agent_id: %s\nstatus: %s\nresult:\n%s",
		job.ID,
		job.TargetAgentID,
		job.Status,
		strings.TrimSpace(result),
	))
}

func notifySchedulerInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper async root scheduler.",
		"You receive scheduler jobs in one of two forms: GOAL JOB or JOB NOTIFICATION.",
		"Use only the goalkeeper.schedule_job tool to enqueue PDCA worker jobs, and goalkeeper.finish to finish the run.",
		"Keep the workflow fixed to plan, then do, then check, then act.",
		"For a GOAL JOB, schedule exactly one plan job and stop.",
		"For a successful plan notification, schedule exactly one do job and stop.",
		"For a successful do notification, schedule exactly one check job and stop.",
		"For a successful check notification, schedule exactly one act job and stop.",
		"For a successful act notification, call goalkeeper.finish with a concise final summary and stop.",
		"If a notification reports an error, call goalkeeper.finish with a concise failure summary and stop.",
		"Do not try to enqueue work directly to another agent without using goalkeeper.schedule_job.",
	}, "\n")
}

// RunNotify executes the async Goalkeeper notification playground.
func RunNotify(ctx context.Context, cfg Config) error {
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	maxToolCalls := cfg.MaxToolCalls
	if maxToolCalls == 0 {
		maxToolCalls = defaultMaxToolCalls
	}
	if maxToolCalls < 0 {
		return fmt.Errorf("max tool calls must be >= 0")
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
		Str("component", "playground.goalkeeper_notify").
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	roleSet, err := newRoleSet(ctx, roleSetConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	defer roleSet.close()

	serviceLogger := logger.With().Str("surface", "notify").Logger()
	service := newNotifyService(serviceLogger, maxToolCalls)
	server, err := startNotifyHTTPServer(ctx, service, "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	scheduler, err := newSchedulerSession(ctx, schedulerSessionConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"goalkeeper": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		return err
	}
	defer scheduler.close()

	runners := map[string]notifyJobRunner{
		schedulerAgentID: scheduler,
		"plan":           roleSet.roles["plan"],
		"do":             roleSet.roles["do"],
		"check":          roleSet.roles["check"],
		"act":            roleSet.roles["act"],
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	coordinator, err := newNotifyCoordinator(logger, runners)
	if err != nil {
		cancel()
		return err
	}
	service.coordinator = coordinator
	coordinator.start(runCtx, runners)

	logger.Info().Str("goal", goal).Msg("goalkeeper notify started")
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
		Msg("goalkeeper notify completed")
	if result.Summary != "" {
		if _, err := fmt.Fprintln(stdout, result.Summary); err != nil {
			return err
		}
	}
	return nil
}
