// Package model defines the core data structures for norma.
package model

// AcceptanceCriterion describes a single acceptance criterion for a run.
type AcceptanceCriterion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	VerifyHints []string `json:"verify_hints,omitempty"`
}

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

	// Legacy/extra budgets (keep for now if needed, but not in new spec input)
	MaxPatchKB      int `json:"max_patch_kb,omitempty"`
	MaxChangedFiles int `json:"max_changed_files,omitempty"`
	MaxRiskyFiles   int `json:"max_risky_files,omitempty"`
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
	Plan  *PlanInput  `json:"plan,omitempty"`
	Do    *DoInput    `json:"do,omitempty"`
	Check *CheckInput `json:"check,omitempty"`
	Act   *ActInput   `json:"act,omitempty"`

	// Legacy fields (optional migration)
	Version int `json:"version,omitempty"`
}

// RunInfo identifies the current run and its iteration.
type RunInfo struct {
	ID        string `json:"id"`
	Iteration int    `json:"iteration"`
}

// TaskInfo contains identification and description info for an issue.
type TaskInfo struct {
	ID                 string                `json:"id"`
	Title              string                `json:"title"`
	Description        string                `json:"description"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
}

// StepInfo identifies the step in the run.
type StepInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"` // "plan", "do", "check", "act"
	Dir   string `json:"dir"`
}

// RequestPaths are absolute paths for agent execution.
type RequestPaths struct {
	WorkspaceDir  string `json:"workspace_dir"`
	WorkspaceMode string `json:"workspace_mode"` // "read_only"
	RunDir        string `json:"run_dir"`
	CodeRoot      string `json:"code_root"`
}

// RequestContext supplies artifacts from previous steps and optional notes.
type RequestContext struct {
	Facts   map[string]any `json:"facts"`
	Links   []string       `json:"links"`
	Journal []JournalEntry `json:"journal,omitempty"`
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
	Logs       ResponseLogs    `json:"logs"`
	Timing     ResponseTiming  `json:"timing"`
	Progress   StepProgress    `json:"progress"`

	// Role-specific outputs
	Plan  *PlanOutput  `json:"plan,omitempty"`
	Do    *DoOutput    `json:"do,omitempty"`
	Check *CheckOutput `json:"check,omitempty"`
	Act   *ActOutput   `json:"act,omitempty"`

	// Legacy fields (optional migration)
	Version int `json:"version,omitempty"`
}

// ResponseSummary captures the outcome of an agent's task.
type ResponseSummary struct {
	Text     string   `json:"text"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

// ResponseLogs provides paths to stdout and stderr logs.
type ResponseLogs struct {
	StdoutPath string `json:"stdout_path"`
	StderrPath string `json:"stderr_path"`
}

// ResponseTiming records the duration of an agent's execution.
type ResponseTiming struct {
	WallTimeMS int64 `json:"wall_time_ms"`
}

// StepProgress captures highlights for the run journal.
type StepProgress struct {
	Title   string            `json:"title"`
	Details []string          `json:"details"`
	Links   map[string]string `json:"links"`
}

// PlanOutput is the primary output from the plan agent.
type PlanOutput struct {
	TaskID             string                 `json:"task_id"`
	Goal               string                 `json:"goal"`
	Constraints        []string               `json:"constraints"`
	AcceptanceCriteria EffectiveCriteriaGroup `json:"acceptance_criteria"`
	WorkPlan           WorkPlan               `json:"work_plan"`
}

// EffectiveCriteriaGroup groups baseline and extended acceptance criteria.
type EffectiveCriteriaGroup struct {
	Baseline  []AcceptanceCriterion          `json:"baseline"`
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
	ID           string    `json:"id"`
	Text         string    `json:"text"`
	Commands     []Command `json:"commands"`
	TargetsACIDs []string  `json:"targets_ac_ids"`
}

// Command represents a single shell command to be executed.
type Command struct {
	ID              string `json:"id"`
	Cmd             string `json:"cmd"`
	ExpectExitCodes []int  `json:"expect_exit_codes"`
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
	Blockers  []Blocker   `json:"blockers"`
}

// DoExecution records the outcome of executed steps and commands.
type DoExecution struct {
	ExecutedStepIDs []string        `json:"executed_step_ids"`
	SkippedStepIDs  []string        `json:"skipped_step_ids"`
	Commands        []CommandResult `json:"commands"`
}

// CommandResult captures the result of a single command execution.
type CommandResult struct {
	ID       string `json:"id"`
	Cmd      string `json:"cmd"`
	ExitCode int    `json:"exit_code"`
}

// Blocker describes an issue that prevented progress.
type Blocker struct {
	Kind                string `json:"kind"` // "dependency", "env", "unknown"
	Text                string `json:"text"`
	SuggestedStopReason string `json:"suggested_stop_reason"`
}

// CheckOutput is the primary output from the check agent.
type CheckOutput struct {
	PlanMatch         PlanMatch          `json:"plan_match"`
	AcceptanceResults []AcceptanceResult `json:"acceptance_results"`
	Verdict           CheckVerdict       `json:"verdict"`
	ProcessNotes      []ProcessNote      `json:"process_notes"`
}

// PlanMatch compares planned vs actual execution.
type PlanMatch struct {
	DoSteps  MatchResult `json:"do_steps"`
	Commands MatchResult `json:"commands"`
}

// MatchResult details the differences between planned and executed IDs.
type MatchResult struct {
	PlannedIDs    []string `json:"planned_ids"`
	ExecutedIDs   []string `json:"executed_ids"`
	MissingIDs    []string `json:"missing_ids"`
	UnexpectedIDs []string `json:"unexpected_ids"`
}

// AcceptanceResult records the pass/fail result for a single AC.
type AcceptanceResult struct {
	ACID   string `json:"ac_id"`
	Result string `json:"result"` // "PASS", "FAIL"
	Notes  string `json:"notes"`
	LogRef string `json:"log_ref"`
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

// ProcessNote provides meta-feedback on the run process.
type ProcessNote struct {
	Kind                string `json:"kind"` // "plan_mismatch", "missing_verification"
	Severity            string `json:"severity"`
	Text                string `json:"text"`
	SuggestedStopReason string `json:"suggested_stop_reason"`
}

// ActOutput is the primary output from the act agent.
type ActOutput struct {
	Decision  string     `json:"decision"` // "close", "replan", "rollback", "continue"
	Rationale string     `json:"rationale"`
	Next      NextAction `json:"next"`
}

// NextAction describes what should happen in the next iteration.
type NextAction struct {
	Recommended bool   `json:"recommended"`
	Notes       string `json:"notes"`
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
	Timestamp  string       `json:"timestamp"`
	StepIndex  int          `json:"step_index"`
	Role       string       `json:"role"`
	Status     string       `json:"status"`
	StopReason string       `json:"stop_reason"`
	Title      string       `json:"title"`
	Details    []string     `json:"details"`
	Logs       ResponseLogs `json:"logs"`
}
