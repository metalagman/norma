package pdcataskmaster

import (
	"context"
	"io"
	"strings"

	"github.com/normahq/norma/internal/apps/taskmastercore"
	"github.com/rs/zerolog"
)

const (
	rootAgentID = "pdca-taskmaster"
)

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

		ComponentName: "playground.pdca_taskmaster",
		SurfaceName:   "pdca-taskmaster",

		RootAgentID: rootAgentID,
		RootAgent: taskmastercore.AgentConfig{
			Name:        "PDCATaskmaster",
			Description: "Strict PDCA async task harness",
			Instruction: rootInstruction(),
		},
		ChildAgents: map[string]taskmastercore.AgentConfig{
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
		DefaultReportTo:           taskmastercore.NewAgentLocator(rootAgentID),
		AllowFinishTool:           true,
		IngressPromptFormatter:    formatIngressPrompt,
		CompletionPromptFormatter: formatCompletionPrompt,
	})
}

func formatIngressPrompt(req taskmastercore.IngressRequest) string {
	return strings.Join([]string{
		"Session ID:",
		strings.TrimSpace(req.SessionID),
		"",
		"Goal:",
		strings.TrimSpace(req.Prompt),
	}, "\n")
}

func formatCompletionPrompt(input taskmastercore.CompletionPromptInput) string {
	lines := []string{
		"Session ID:",
		strings.TrimSpace(input.SessionID),
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

func rootInstruction() string {
	return strings.Join([]string{
		"You are the PDCA Taskmaster async root agent named pdca-taskmaster.",
		"You receive only prompt text as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You are running a strict PDCA workflow over child agents.",
		"Run phases in this exact order for each iteration: plan -> do -> check -> act.",
		"Always start a new goal with plan. Do not skip phases and do not reorder them.",
		"Use only the taskmaster.schedule_task tool to enqueue child-agent tasks, and taskmaster.finish to finish the run.",
		"Each scheduled task must include a stable task_id, the current session_id, a locator, an optional report_to, and a prompt.",
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
