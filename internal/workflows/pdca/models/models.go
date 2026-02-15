// Package models defines shared request/response contracts for pdca.
package models

import (
	"github.com/metalagman/norma/internal/task"
	pdcact "github.com/metalagman/norma/internal/workflows/pdca/roles/act"
	pdcacheck "github.com/metalagman/norma/internal/workflows/pdca/roles/check"
	pdcado "github.com/metalagman/norma/internal/workflows/pdca/roles/do"
	pdcaplan "github.com/metalagman/norma/internal/workflows/pdca/roles/plan"
)

// Generated contract aliases. The schema-generated structs are the source of truth.
type EffectiveAcceptanceCriterion = pdcaplan.EffectiveAC
type Check = pdcaplan.ACCheck
type PlanOutput = pdcaplan.PlanOutput
type EffectiveCriteriaGroup = pdcaplan.PlanAcceptanceCriteria
type WorkPlan = pdcaplan.PlanWorkPlan
type DoStep = pdcaplan.PlanDoStep
type CheckStep = pdcaplan.PlanCheckStep
type DoOutput = pdcado.DoOutput
type DoExecution = pdcado.DoExecution
type CheckOutput = pdcacheck.CheckOutput
type AcceptanceResult = pdcacheck.CheckAcceptanceResult
type CheckVerdict = pdcacheck.CheckVerdict
type Basis = pdcacheck.CheckVerdictBasis
type ActOutput = pdcact.ActOutput

// Budgets defines run budgets.
type Budgets struct {
	MaxIterations      int `json:"max_iterations"`
	MaxWallTimeMinutes int `json:"max_wall_time_minutes,omitempty"`
	MaxFailedChecks    int `json:"max_failed_checks,omitempty"`
}

// AgentRequest is the normalized request passed to agents.
type AgentRequest struct {
	Run     RunInfo        `json:"run"`
	Task    TaskInfo       `json:"task"`
	Step    StepInfo       `json:"step"`
	Paths   RequestPaths   `json:"paths"`
	Budgets Budgets        `json:"budgets"`
	Context RequestContext `json:"context"`

	StopReasonsAllowed []string `json:"stop_reasons_allowed"`

	// Role-specific inputs
	Plan  *PlanInput  `json:"plan_input,omitempty"`
	Do    *DoInput    `json:"do_input,omitempty"`
	Check *CheckInput `json:"check_input,omitempty"`
	Act   *ActInput   `json:"act_input,omitempty"`
}

// RunInfo identifies the current run and its iteration.
type RunInfo struct {
	ID        string `json:"id"`
	Iteration int    `json:"iteration"`
}

// TaskInfo contains identification and description info for an issue.
type TaskInfo struct {
	ID                 string                     `json:"id"`
	Title              string                     `json:"title"`
	Description        string                     `json:"description"`
	AcceptanceCriteria []task.AcceptanceCriterion `json:"acceptance_criteria"`
}

// StepInfo identifies the step in the run.
type StepInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"` // "plan", "do", "check", "act"
}

// RequestPaths are absolute paths for agent execution.
type RequestPaths struct {
	WorkspaceDir string `json:"workspace_dir"`
	RunDir       string `json:"run_dir"`
	Progress     string `json:"progress"`
}

// RequestContext supplies artifacts from previous steps and optional notes.
type RequestContext struct {
	Facts   map[string]any `json:"facts"`
	Links   []string       `json:"links"`
	Attempt int            `json:"attempt,omitempty"`
}

// PlanInput provides role-specific context for the plan agent.
type PlanInput struct {
	Task IDInfo `json:"task"`
}

// DoInput provides role-specific context for the do agent.
type DoInput struct {
	WorkPlan          WorkPlan                       `json:"work_plan"`
	EffectiveCriteria []EffectiveAcceptanceCriterion `json:"acceptance_criteria_effective"`
}

// CheckInput provides role-specific context for the check agent.
type CheckInput struct {
	WorkPlan          WorkPlan                       `json:"work_plan"`
	EffectiveCriteria []EffectiveAcceptanceCriterion `json:"acceptance_criteria_effective"`
	DoExecution       DoExecution                    `json:"do_execution"`
}

// ActInput provides role-specific context for the act agent.
type ActInput struct {
	CheckVerdict      CheckVerdict       `json:"check_verdict"`
	AcceptanceResults []AcceptanceResult `json:"acceptance_results,omitempty"`
}

// IDInfo contains identification info for an issue.
type IDInfo struct {
	ID string `json:"id"`
}

// AgentResponse is the normalized stdout response from agents.
type AgentResponse struct {
	Status     string          `json:"status"` // "ok", "stop", "error"
	StopReason string          `json:"stop_reason,omitempty"`
	Summary    ResponseSummary `json:"summary"`
	Progress   StepProgress    `json:"progress"`

	// Role-specific outputs
	Plan  *PlanOutput  `json:"plan_output,omitempty"`
	Do    *DoOutput    `json:"do_output,omitempty"`
	Check *CheckOutput `json:"check_output,omitempty"`
	Act   *ActOutput   `json:"act_output,omitempty"`
}

// ResponseSummary captures the outcome of an agent's task.
type ResponseSummary struct {
	Text string `json:"text"`
}

// StepProgress captures highlights for the run journal.
type StepProgress struct {
	Title   string   `json:"title"`
	Details []string `json:"details"`
}

// TaskState is stored in task notes to persist step outputs and journal across runs.
type TaskState struct {
	Plan    *PlanOutput    `json:"plan,omitempty"`
	Do      *DoOutput      `json:"do,omitempty"`
	Check   *CheckOutput   `json:"check,omitempty"`
	Act     *ActOutput     `json:"act,omitempty"`
	Journal []JournalEntry `json:"journal,omitempty"`
}

// JournalEntry records detailed progress for a single step.
type JournalEntry struct {
	Timestamp  string   `json:"timestamp"`
	RunID      string   `json:"run_id,omitempty"`
	Iteration  int      `json:"iteration,omitempty"`
	StepIndex  int      `json:"step_index"`
	Role       string   `json:"role"`
	Status     string   `json:"status"`
	StopReason string   `json:"stop_reason"`
	Title      string   `json:"title"`
	Details    []string `json:"details"`
}
