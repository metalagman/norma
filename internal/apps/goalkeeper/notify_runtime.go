package goalkeeper

import (
	"context"
	"encoding/json"
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
	defaultQueueDepth    = 32
	initialGoalJobID     = "goal-job"
	defaultMaxAttempts   = 1
	notifyGoalkeeperName = "GoalkeeperNotifyGoalkeeper"
)

type notifyJobKind string

const (
	notifyJobKindAgent      notifyJobKind = "agent"
	notifyJobKindGoalkeeper notifyJobKind = "goalkeeper"
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
	Locator       jobLocator
	ReplyTo       jobLocator
	SourceJobID   string
	SourceLocator *jobLocator
	Input         string
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
	logger := cfg.Logger.With().Str("agent_id", goalkeeperAgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              notifyGoalkeeperName,
		Description:       "Goalkeeper async root agent",
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
		return nil, fmt.Errorf("create goalkeeper agent: %w", err)
	}
	sessionService := session.InMemoryService()
	const appName = "goalkeeper-notify-goalkeeper"
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create goalkeeper runner: %w", err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    "goalkeeper",
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create goalkeeper session: %w", err)
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
			if job.Kind == notifyJobKindGoalkeeper && job.SourceJobID != "" {
				e.logger.Debug().
					Str("job_id", job.ID).
					Str("source_job_id", job.SourceJobID).
					Interface("source_locator", job.SourceLocator).
					Interface("reply_to", job.ReplyTo).
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

	mu           sync.Mutex
	jobs         map[string]*notifyJob
	queues       map[string]chan *notifyJob
	terminal     bool
	finalSummary string
	finalErr     error
	done         chan notifyRunResult
	doneClosed   bool

	wg sync.WaitGroup
}

func newNotifyCoordinator(logger zerolog.Logger, runners map[string]notifyJobRunner) (*notifyCoordinator, error) {
	for _, agentID := range append([]string{goalkeeperAgentID}, childAgentIDs()...) {
		if runners[agentID] == nil {
			return nil, fmt.Errorf("missing runner for agent %q", agentID)
		}
	}
	c := &notifyCoordinator{
		logger: logger,
		jobs:   make(map[string]*notifyJob),
		queues: make(map[string]chan *notifyJob),
		done:   make(chan notifyRunResult, 1),
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
		ID:          initialGoalJobID,
		Kind:        notifyJobKindGoalkeeper,
		Locator:     newAgentLocator(goalkeeperAgentID),
		ReplyTo:     newAgentLocator(goalkeeperAgentID),
		Input:       "GOAL JOB:\n" + strings.TrimSpace(goal),
		Status:      notifyJobStatusQueued,
		Attempt:     1,
		MaxAttempts: defaultMaxAttempts,
	}
	return c.enqueueJob(job)
}

func (c *notifyCoordinator) scheduleJob(jobID string, locator jobLocator, replyTo jobLocator, task string) error {
	jobID = strings.TrimSpace(jobID)
	task = strings.TrimSpace(task)
	if jobID == "" {
		return errors.New("job_id is required")
	}
	if task == "" {
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
	if _, exists := c.jobs[jobID]; exists {
		return fmt.Errorf("job %q already exists", jobID)
	}
	job := c.newQueuedJobLocked(jobID, notifyJobKindAgent, locator, replyTo, task)
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
	queued := c.newQueuedJobLocked(job.ID, job.Kind, job.Locator, job.ReplyTo, job.Input)
	queued.SourceJobID = job.SourceJobID
	queued.SourceLocator = job.SourceLocator
	return c.enqueueQueuedJobLocked(queued)
}

func (c *notifyCoordinator) newQueuedJobLocked(jobID string, kind notifyJobKind, locator jobLocator, replyTo jobLocator, input string) *notifyJob {
	now := time.Now().UTC()
	job := &notifyJob{
		ID:          jobID,
		Kind:        kind,
		Locator:     locator,
		ReplyTo:     replyTo,
		Input:       input,
		Status:      notifyJobStatusQueued,
		Attempt:     1,
		MaxAttempts: defaultMaxAttempts,
		CreatedAt:   now,
		ScheduledAt: now,
	}
	c.jobs[jobID] = job
	return job
}

func (c *notifyCoordinator) enqueueQueuedJobLocked(job *notifyJob) error {
	queue, ok := c.queues[job.Locator.ID]
	if !ok {
		return fmt.Errorf("unknown locator.id %q", job.Locator.ID)
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

	if job.Locator.ID == goalkeeperAgentID {
		if err != nil {
			c.finalErr = fmt.Errorf("goalkeeper job %q failed: %w", job.ID, err)
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

	if !c.terminal {
		sourceLocator := job.Locator
		notification = c.newQueuedJobLocked(
			job.ID+".notify",
			notifyJobKindGoalkeeper,
			job.ReplyTo,
			newAgentLocator(goalkeeperAgentID),
			formatNotificationJobInput(job),
		)
		notification.SourceJobID = job.ID
		notification.SourceLocator = &sourceLocator
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
	queue, ok := c.queues[job.Locator.ID]
	if !ok {
		return fmt.Errorf("unknown locator.id %q", job.Locator.ID)
	}
	c.logJobEvent("job enqueued", job)
	queue <- job
	return nil
}

func (c *notifyCoordinator) logJobEvent(message string, job *notifyJob) {
	event := c.logger.Debug().
		Str("job_id", job.ID).
		Str("kind", string(job.Kind)).
		Interface("locator", job.Locator).
		Interface("reply_to", job.ReplyTo).
		Str("status", string(job.Status))
	if job.SourceJobID != "" {
		event = event.Str("source_job_id", job.SourceJobID)
	}
	if job.SourceLocator != nil {
		event = event.Interface("source_locator", job.SourceLocator)
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
	type completionEnvelope struct {
		Type          string     `json:"type"`
		SourceJobID   string     `json:"source_job_id"`
		SourceLocator jobLocator `json:"source_locator"`
		ReplyTo       jobLocator `json:"reply_to"`
		Status        string     `json:"status"`
		Result        string     `json:"result,omitempty"`
		Error         string     `json:"error,omitempty"`
	}

	envelope := completionEnvelope{
		Type:          "job_completion",
		SourceJobID:   job.ID,
		SourceLocator: job.Locator,
		ReplyTo:       job.ReplyTo,
		Status:        string(job.Status),
	}
	if job.Error != "" {
		envelope.Error = job.Error
	} else {
		envelope.Result = job.Output
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return strings.TrimSpace(fmt.Sprintf("JOB ENVELOPE:\n%s", job.ID))
	}
	return "JOB ENVELOPE:\n" + string(payload)
}

func notifySchedulerInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper async root agent named goalkeeper.",
		"You receive goalkeeper jobs in one of two forms: GOAL JOB or JOB ENVELOPE.",
		"Use only the goalkeeper.schedule_job tool to enqueue child-agent jobs, and goalkeeper.finish to finish the run.",
		"Each scheduled job must include a stable job_id, a locator, an optional reply_to, and task text.",
		"The child agents available in this MVP are plan, do, check, and act.",
		"Decide yourself which child agent to run next based on the goal and the job envelopes you receive.",
		"If the goal is handled, call goalkeeper.finish with a concise final summary.",
		"If a job envelope reports an error and you want to stop, call goalkeeper.finish with a concise failure summary.",
		"Do not try to deliver work directly without using goalkeeper.schedule_job.",
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
		goalkeeperAgentID: scheduler,
		"plan":            roleSet.roles["plan"],
		"do":              roleSet.roles["do"],
		"check":           roleSet.roles["check"],
		"act":             roleSet.roles["act"],
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
