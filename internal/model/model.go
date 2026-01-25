package model

// AcceptanceCriterion describes a single acceptance criterion for a run.
type AcceptanceCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Budgets defines run budgets.
type Budgets struct {
	MaxIterations   int `json:"max_iterations"`
	MaxPatchKB      int `json:"max_patch_kb,omitempty"`
	MaxChangedFiles int `json:"max_changed_files,omitempty"`
	MaxRiskyFiles   int `json:"max_risky_files,omitempty"`
}

// AgentRequest is the normalized request passed to agents.
type AgentRequest struct {
	Version int            `json:"version"`
	RunID   string         `json:"run_id"`
	Step    StepInfo       `json:"step"`
	Goal    string         `json:"goal"`
	Norma   NormaInfo      `json:"norma"`
	Paths   RequestPaths   `json:"paths"`
	Context RequestContext `json:"context"`
	Plan    *PlanContext   `json:"plan,omitempty"`
	Do      *DoContext     `json:"do,omitempty"`
}

// PlanContext provides role-specific context for the plan agent.
type PlanContext struct {
	Issue IDInfo `json:"issue"`
}

// DoContext provides role-specific context for the do agent.
type DoContext struct {
	Issue IDInfo `json:"issue"`
}

// IDInfo contains identification info for an issue.
type IDInfo struct {
	ID string `json:"id"`
}

// StepInfo identifies the step in the run.
type StepInfo struct {
	Index     int    `json:"index"`
	Role      string `json:"role"`
	Iteration int    `json:"iteration"`
}

// NormaInfo captures acceptance criteria and budgets for the run.
type NormaInfo struct {
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
	Budgets            Budgets               `json:"budgets"`
}

// RequestPaths are absolute paths for agent execution.
type RequestPaths struct {
	RepoRoot string `json:"repo_root"`
	RunDir   string `json:"run_dir"`
	StepDir  string `json:"step_dir"`
}

// RequestContext supplies artifacts from previous steps and optional notes.
type RequestContext struct {
	Artifacts   []string `json:"artifacts,omitempty"`
	NextActions []string `json:"next_actions,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

// AgentResponse is the normalized stdout response from agents.
type AgentResponse struct {
	Version     int      `json:"version"`
	Status      string   `json:"status"`
	Summary     string   `json:"summary"`
	Files       []string `json:"files"`
	NextActions []string `json:"next_actions"`
	Errors      []string `json:"errors"`
}

// Verdict is the required output for the check role.
type Verdict struct {
	Version        int               `json:"version"`
	Verdict        string            `json:"verdict"`
	Criteria       []VerdictCriteria `json:"criteria"`
	Metrics        map[string]any    `json:"metrics"`
	Blockers       []string          `json:"blockers"`
	RecommendedFix []string          `json:"recommended_fix"`
}

// VerdictCriteria captures pass/fail info for each criterion.
type VerdictCriteria struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Pass     bool   `json:"pass"`
	Evidence string `json:"evidence"`
}
