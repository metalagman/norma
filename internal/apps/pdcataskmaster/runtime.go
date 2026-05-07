package pdcataskmaster

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	taskmasteradk "github.com/normahq/norma/pkg/runtime/taskmaster/adk"
	taskmastermcp "github.com/normahq/norma/pkg/runtime/taskmaster/mcp"
	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
)

const rootAgentID = "pdca-taskmaster"

type Config struct {
	Goal       string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

var childAgentInstructions = map[string]string{
	"plan": strings.Join([]string{
		"You are the plan phase of a strict PDCA flow.",
		"Work only on planning for the current iteration.",
		"Produce the next concise plain-text plan that the do phase should execute.",
		"Do not execute work, check results, or act on outcomes.",
		"Do not use JSON, schemas, field names, or code fences.",
		"Return only the useful planning result as plain text.",
	}, "\n"),
	"do": strings.Join([]string{
		"You are the do phase of a strict PDCA flow.",
		"Execute only the assigned plan for the current iteration.",
		"Do not replan, verify completion, or choose the next action.",
		"Do not use JSON, schemas, field names, or code fences.",
		"Return only the useful execution result for the check phase as plain text.",
	}, "\n"),
	"check": strings.Join([]string{
		"You are the check phase of a strict PDCA flow.",
		"Compare the execution result against the plan and the goal.",
		"Return a concise plain-text assessment of whether the iteration passed or failed, with brief evidence.",
		"You may start with `verdict: pass` or `verdict: fail`, but this is optional.",
		"Do not act, replan, or execute more work.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
	"act": strings.Join([]string{
		"You are the act phase of a strict PDCA flow.",
		"Consume only the check result for the current iteration.",
		"Your output is advisory input for the root taskmaster agent.",
		"Return a concise plain-text recommendation for whether the run should close or replan, with a brief reason.",
		"You may start with `decision: close` or `decision: replan`, but this is optional.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
}

func BuildCodexACPCommand(bridgeBin string) []string {
	return taskmasteradk.BuildCodexACPCommand(bridgeBin)
}

func Run(ctx context.Context, cfg Config) error {
	baseLogger := *zerolog.Ctx(ctx)
	if cfg.Logger != nil {
		baseLogger = *cfg.Logger
	}

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	command := taskmasteradk.BuildCodexACPCommand(cfg.BridgeBin)
	childRunners, err := taskmasteradk.NewRunnerSet(ctx, taskmasteradk.RunnerSetConfig{
		RootAgentID: rootAgentID,
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      stderr,
		Logger:      baseLogger,
		ChildAgents: map[string]taskmasterrt.AgentConfig{
			"plan": {
				Name:        "PDCATaskmasterPlan",
				Description: "PDCA plan child agent",
				Instruction: childAgentInstructions["plan"],
			},
			"do": {
				Name:        "PDCATaskmasterDo",
				Description: "PDCA do child agent",
				Instruction: childAgentInstructions["do"],
			},
			"check": {
				Name:        "PDCATaskmasterCheck",
				Description: "PDCA check child agent",
				Instruction: childAgentInstructions["check"],
			},
			"act": {
				Name:        "PDCATaskmasterAct",
				Description: "PDCA act child agent",
				Instruction: childAgentInstructions["act"],
			},
		},
	})
	if err != nil {
		return err
	}

	serviceLogger := baseLogger.With().Str("surface", "pdca-taskmaster").Logger()
	service := taskmastermcp.NewService(serviceLogger, taskmasterrt.NewAgentLocator(rootAgentID), true)
	server, err := taskmastermcp.StartHTTPServer(ctx, service, "127.0.0.1:0")
	if err != nil {
		for _, runner := range childRunners {
			_ = runner.Close()
		}
		return err
	}

	rootRunner, err := taskmasteradk.NewRunner(ctx, taskmasteradk.RunnerConfig{
		AgentID:     rootAgentID,
		AppName:     "taskmaster-" + rootAgentID,
		Name:        "PDCATaskmaster",
		Description: "Strict PDCA async task harness",
		Instruction: rootInstruction(),
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      stderr,
		Logger:      baseLogger,
		UserID:      rootAgentID,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"taskmaster": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		_ = server.Close()
		for _, runner := range childRunners {
			_ = runner.Close()
		}
		return err
	}

	localRunners := map[string]taskmasterrt.LocalRunner{rootAgentID: rootRunner}
	for id, runner := range childRunners {
		localRunners[id] = runner
	}

	runtime, err := taskmasterrt.Start(ctx, taskmasterrt.Config{
		Logger:                     &baseLogger,
		RootAgentID:                rootAgentID,
		LocalRunners:               localRunners,
		DefaultReportTo:            taskmasterrt.NewAgentLocator(rootAgentID),
		ReportTaskContentFormatter: formatReportTaskContent,
		Closers:                    []io.Closer{server},
	})
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()
	service.SetController(runtime)

	if err := runtime.Enqueue(taskmasterrt.Task{
		ID:        "goal-task",
		SessionID: "goal-task",
		Locator:   taskmasterrt.NewAgentLocator(rootAgentID),
		Content:   formatIngressContent("goal-task", cfg.Goal),
	}); err != nil {
		return err
	}

	startedAt := time.Now()
	result, err := runtime.Wait()
	if err != nil {
		return err
	}
	return taskmasterrt.WriteRunOutput(stdout, result.Summary, taskmasterrt.FormatElapsed(time.Since(startedAt)))
}

func formatIngressContent(sessionID string, content string) string {
	return strings.Join([]string{
		"Session ID:",
		strings.TrimSpace(sessionID),
		"",
		"Goal:",
		strings.TrimSpace(content),
	}, "\n")
}

func formatReportTaskContent(source taskmasterrt.Task, output string, err error) string {
	lines := []string{
		"Session ID:",
		strings.TrimSpace(source.SessionID),
		"",
	}
	if err != nil {
		lines = append(lines,
			"Task "+strings.TrimSpace(source.ID)+" failed.",
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
		"Task "+strings.TrimSpace(source.ID)+" completed.",
		"",
		"Result:",
		result,
	)
	return strings.Join(lines, "\n")
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the PDCA Taskmaster async root agent named pdca-taskmaster.",
		"You receive only plain-text task content as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You are running a strict PDCA workflow over child agents.",
		"Run phases in this exact order for each iteration: plan -> do -> check -> act.",
		"Always start a new goal with plan. Do not skip phases and do not reorder them.",
		"Use only the taskmaster.schedule_task tool to enqueue child-agent tasks, and taskmaster.finish to finish the run.",
		"Each scheduled task must include a stable task_id, the current session_id, a locator, an optional report_to, and content.",
		"Keep the same session_id when continuing the same PDCA conversation.",
		"The report_to field means where task completion should be reported.",
		"The local root agent locator is {class: agent, transport: local, key: pdca-taskmaster}.",
		"The child agent locators are local agent locators with ids plan, do, check, and act.",
		"The child agents available in this wrapper are plan, do, check, and act.",
		"Treat plan, do, check, and act as strict PDCA phases, not generic workers.",
		"After a plan completion, schedule do. After a do completion, schedule check. After a check completion, schedule act.",
		"When handing work to a child agent, do not author task-specific methodology, examples, commands, acceptance criteria, or execution instructions yourself.",
		"The child agent's own system prompt defines how that phase works.",
		"For plan, pass only the raw goal text.",
		"For do, pass only the prior plan output.",
		"For check, pass only neutral sections with the raw upstream texts: Goal:, Plan output:, Do output:.",
		"For act, pass only a neutral Check output: section.",
		"Neutral section headers are allowed only to separate raw prior texts. Do not add new guidance around them.",
		"Child agents return freeform plain text, not structured role payloads.",
		"Do not expect JSON, field names, or code fences from child agents.",
		"You interpret check and act outputs semantically from their plain text.",
		"If a child output happens to include helpful labels like `verdict:` or `decision:`, you may use them, but do not require them.",
		"If an act output clearly recommends close, call taskmaster.finish with a concise final summary.",
		"If an act output clearly recommends replan, more planning is required before further execution.",
		"You decide the next child task yourself from the prompt text you receive. Do not treat child outputs as direct runtime commands.",
		"If a completion prompt reports failure and you want to stop, call taskmaster.finish with a concise failure summary.",
		"Do not read files, execute scripts, or perform worker work yourself.",
		"Only coordinate the PDCA flow through child-agent tasks.",
		"Do not try to deliver work directly without using taskmaster.schedule_task.",
	}, "\n")
}
