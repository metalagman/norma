package taskmaster

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/normahq/norma/internal/apps/taskmastercore"
	"github.com/rs/zerolog"
)

const (
	taskmasterAgentID  = "taskmaster"
	workerAgentID      = "worker"
	timerGoalMessage   = "hello world"
	timerGoalInterval  = 30 * time.Second
	defaultAgentName   = "Taskmaster"
	defaultWorkerName  = "TaskmasterWorker"
	defaultDescription = "Workflow-agnostic async task harness"
)

type Config struct {
	Goal       string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

var workerInstruction = strings.Join([]string{
	"You are a generic plain-text worker in an async task harness.",
	"Execute the assigned prompt as directly as you can.",
	"Return only the useful result as plain text.",
	"Do not use JSON, schemas, field names, or code fences unless the prompt explicitly requires them.",
}, "\n")

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type realTicker struct {
	*time.Ticker
}

func (t realTicker) Chan() <-chan time.Time { return t.C }

var newTicker = func(d time.Duration) ticker {
	return realTicker{Ticker: time.NewTicker(d)}
}

func BuildCodexACPCommand(bridgeBin string) []string {
	return taskmastercore.BuildCodexACPCommand(bridgeBin)
}

func Run(ctx context.Context, cfg Config) error {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	baseLogger := *zerolog.Ctx(runCtx)
	if cfg.Logger != nil {
		baseLogger = *cfg.Logger
	}

	return taskmastercore.Run(runCtx, taskmastercore.Config{
		Goal:       cfg.Goal,
		WorkingDir: cfg.WorkingDir,
		BridgeBin:  cfg.BridgeBin,
		Stdout:     cfg.Stdout,
		Stderr:     cfg.Stderr,
		Logger:     cfg.Logger,

		ComponentName: "playground.taskmaster",
		SurfaceName:   "taskmaster",

		RootAgentID: taskmasterAgentID,
		RootAgent: taskmastercore.AgentConfig{
			Name:        defaultAgentName,
			Description: defaultDescription,
			Instruction: rootInstruction(),
		},
		ChildAgents: map[string]taskmastercore.AgentConfig{
			workerAgentID: {
				Name:        defaultWorkerName,
				Description: "Generic async worker child agent",
				Instruction: workerInstruction,
			},
		},
		DefaultReportTo:           taskmastercore.NewAgentLocator(taskmasterAgentID),
		AllowFinishTool:           false,
		FinishOnContextDone:       true,
		Providers:                 []taskmastercore.Provider{taskmastercore.NewCLILogProvider(baseLogger)},
		IngressPromptFormatter:    formatIngressPrompt,
		CompletionPromptFormatter: formatCompletionPrompt,
		BackgroundGoalSource:      backgroundGoalSource(timerGoalInterval, newTicker),
	})
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the generic Taskmaster async root agent named taskmaster.",
		"You receive only prompt text as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You coordinate one plain-text child agent named worker.",
		"Use only the taskmaster.schedule_task tool to enqueue worker tasks.",
		"Each scheduled task must include a stable task_id, the current session_id, a locator, an optional report_to, and a prompt.",
		"Keep the same session_id when scheduling follow-up work for the same conversation.",
		"The only local child locator in this wrapper is {class: agent, transport: local, key: worker}.",
		"The report_to field uses the same locator schema as the target.",
		"The local root agent is {class: agent, transport: local, key: taskmaster}.",
		"The current log sink is {class: integration, transport: cli, key: log}.",
		"If you use report_to as the current log sink, that completion goes only to the current log and is not returned to you for further orchestration.",
		"Use cli log reporting only when you do not need the completion text to decide what to do next.",
		"The CLI ingress source is {class: integration, transport: cli, key: input}.",
		"The background timer source is {class: integration, transport: timer, key: default}.",
		"A background timer may also deliver simple hello world goals to you while the run is active.",
		"This generic run does not finish on your turn completion.",
		"It ends only when the host context is canceled, typically by a process signal such as SIGINT or SIGTERM.",
		"Keep coordinating work and updating your current best summary in plain text while the run stays active.",
		"Do not impose a fixed workflow or phase order on the work.",
		"Pass only the minimal plain-text context needed for the worker task.",
		"Do not invent extra routing protocol, schemas, or structured envelopes in prompts.",
		"The worker returns freeform plain text. Do not require JSON, field names, or code fences unless the task itself calls for them.",
		"Do not do worker work yourself. Only coordinate tasks through the worker.",
	}, "\n")
}

func formatIngressPrompt(req taskmastercore.IngressRequest) string {
	return strings.Join([]string{
		"Session ID:",
		strings.TrimSpace(req.SessionID),
		"",
		"Source:",
		locatorText(req.Source),
		"",
		"Prompt:",
		strings.TrimSpace(req.Prompt),
	}, "\n")
}

func formatCompletionPrompt(input taskmastercore.CompletionPromptInput) string {
	lines := []string{
		"Session ID:",
		strings.TrimSpace(input.SessionID),
		"",
		"Source:",
		locatorText(input.Source),
		"",
	}
	if input.Error != "" {
		lines = append(lines,
			"Task "+strings.TrimSpace(input.TaskID)+" failed.",
			"",
			"Error:",
			strings.TrimSpace(input.Error),
		)
		return strings.Join(lines, "\n")
	}
	result := strings.TrimSpace(input.Output)
	if result == "" {
		result = "(empty result)"
	}
	lines = append(lines,
		"Task "+strings.TrimSpace(input.TaskID)+" completed.",
		"",
		"Result:",
		result,
	)
	return strings.Join(lines, "\n")
}

func locatorText(locator taskmastercore.Locator) string {
	return strings.Join([]string{locator.Class, locator.Transport, locator.Key}, "/")
}

func backgroundGoalSource(interval time.Duration, makeTicker func(time.Duration) ticker) taskmastercore.BackgroundGoalSource {
	return func(ctx context.Context, enqueue func(taskmastercore.IngressRequest) error) {
		t := makeTicker(interval)
		defer t.Stop()
		counter := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.Chan():
				counter++
				id := "timer-" + strconv.Itoa(counter)
				_ = enqueue(taskmastercore.IngressRequest{
					ID:        id,
					SessionID: id,
					Prompt:    timerGoalMessage,
					Source:    taskmastercore.NewTimerSourceLocator(),
				})
			}
		}
	}
}
