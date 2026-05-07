package taskmaster

import (
	"context"
	"io"
	"strings"
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
	return taskmastercore.Run(ctx, taskmastercore.Config{
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
		DefaultReportTo:      taskmastercore.NewAgentLocator(taskmasterAgentID),
		AllowHumanOutputSink: true,
		GoalPromptFormatter:  formatGoalPrompt,
		BackgroundGoalSource: backgroundGoalSource(timerGoalInterval, newTicker),
	})
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the generic Taskmaster async root agent named taskmaster.",
		"You receive only prompt text as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You coordinate one plain-text child agent named worker.",
		"Use only the taskmaster.schedule_task tool to enqueue worker tasks, and taskmaster.finish to finish the run.",
		"Each scheduled task must include a stable task_id, a locator, an optional report_to, and a prompt.",
		"The only child locator in this wrapper is worker.",
		"The report_to field may target either the taskmaster root agent or human_output current_log.",
		"If you use report_to as human_output current_log, that completion goes only to the current log and is not returned to you for further orchestration.",
		"Use human_output reporting only when you do not need the completion text to decide what to do next.",
		"A background timer may also deliver simple hello world goals to you while the run is active.",
		"Do not impose a fixed workflow or phase order on the work.",
		"Pass only the minimal plain-text context needed for the worker task.",
		"Do not invent extra routing protocol, schemas, or structured envelopes in prompts.",
		"The worker returns freeform plain text. Do not require JSON, field names, or code fences unless the task itself calls for them.",
		"When you are done, call taskmaster.finish with a concise final summary.",
		"Do not do worker work yourself. Only coordinate tasks through the worker.",
	}, "\n")
}

func formatGoalPrompt(goal string) string {
	return strings.Join([]string{
		"Goal:",
		strings.TrimSpace(goal),
	}, "\n")
}

func backgroundGoalSource(interval time.Duration, makeTicker func(time.Duration) ticker) taskmastercore.BackgroundGoalSource {
	return func(ctx context.Context, enqueue func(string) error) {
		t := makeTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.Chan():
				_ = enqueue(timerGoalMessage)
			}
		}
	}
}
