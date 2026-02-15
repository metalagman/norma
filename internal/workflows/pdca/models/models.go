// Package models defines shared request/response contracts for pdca.
package models

import (
	"github.com/metalagman/norma/internal/task"
)

// EffectiveAcceptanceCriterion represents ACs refined by the Plan agent.
type EffectiveAcceptanceCriterion struct {
	ID      string   `json:"id"`
	Origin  string   `json:"origin"` // "baseline" or "extended"
	Refines []string `json:"refines,omitempty"`
	Text    string   `json:"text"`
	Checks  []Check  `json:"checks"`
	Reason  string   `json:"reason,omitempty"`
}

// Check defines a specific verification command.
type Check struct {
	ID              string `json:"id"`
	Cmd             string `json:"cmd"`
	ExpectExitCodes []int  `json:"expect_exit_codes"`
}

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

// PlanOutput is the primary output from the plan agent.
type PlanOutput struct {
	AcceptanceCriteria EffectiveCriteriaGroup `json:"acceptance_criteria"`
	WorkPlan           WorkPlan               `json:"work_plan"`
}

// EffectiveCriteriaGroup groups baseline and extended acceptance criteria.
type EffectiveCriteriaGroup struct {
	Effective []EffectiveAcceptanceCriterion `json:"effective"`
}

// WorkPlan outlines the steps for implementing and verifying a task.
type WorkPlan struct {
	TimeboxMinutes int         `json:"timebox_minutes"`
	DoSteps        []DoStep    `json:"do_steps"`
	CheckSteps     []CheckStep `json:"check_steps"`
	StopTriggers   []string    `json:"stop_triggers"`
}

// DoStep defines an implementation step.
type DoStep struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	TargetsACIDs []string `json:"targets_ac_ids"`
}

// CheckStep defines a verification step.
type CheckStep struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Mode string `json:"mode"` // "acceptance_criteria"
}

// DoOutput is the primary output from the do agent.
type DoOutput struct {
	Execution DoExecution `json:"execution"`
}

// DoExecution records the outcome of executed steps.
type DoExecution struct {
	ExecutedStepIDs []string `json:"executed_step_ids"`
	SkippedStepIDs  []string `json:"skipped_step_ids"`
}

// CheckOutput is the primary output from the check agent.
type CheckOutput struct {
	AcceptanceResults []AcceptanceResult `json:"acceptance_results"`
	Verdict           CheckVerdict       `json:"verdict"`
}

// AcceptanceResult records the pass/fail result for a single AC.
type AcceptanceResult struct {
	ACID   string `json:"ac_id"`
	Result string `json:"result"` // "PASS", "FAIL"
	Notes  string `json:"notes"`
}

// CheckVerdict summarizes the outcome of the Check step.
type CheckVerdict struct {
	Status         string `json:"status"`         // "PASS", "FAIL", "PARTIAL"
	Recommendation string `json:"recommendation"` // "standardize", "replan", "rollback", "continue"
	Basis          Basis  `json:"basis"`
}

// Basis explains the rationale for a verdict.
type Basis struct {
	PlanMatch           string `json:"plan_match"` // "MATCH", "MISMATCH"
	AllAcceptancePassed bool   `json:"all_acceptance_passed"`
}

// ActOutput is the primary output from the act agent.
type ActOutput struct {
	Decision string `json:"decision"` // "close", "replan", "rollback", "continue"
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
