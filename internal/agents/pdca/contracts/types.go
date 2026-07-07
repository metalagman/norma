package contracts

import (
	"encoding/json"

	"github.com/normahq/norma/v2/internal/task"
)

// RawAgentRequest is the raw JSON bytes passed to role MapRequest implementations.
type RawAgentRequest = json.RawMessage

// SchemaPair holds input and output JSON schemas for a role.
type SchemaPair struct {
	InputSchema  string
	OutputSchema string
}

// AgentRequest is the normalized request passed to agents.
// Each role reads what it needs from TaskState.
type AgentRequest struct {
	Run   RunInfo      `json:"run"`
	Task  TaskInfo     `json:"task"`
	Step  StepInfo     `json:"step"`
	Paths RequestPaths `json:"paths"`

	// TaskState contains outputs from all previous roles.
	// Each role reads what it needs from this shared state.
	TaskState TaskState `json:"task_state"`
}

// RunInfo identifies the current run and its iteration.
type RunInfo struct {
	ID        string `json:"id"`
	Iteration int    `json:"iteration"`
}

// TaskInfo contains identification and description info for an issue.
type TaskInfo struct {
	ID                 string                     `json:"id"`
	Goal               string                     `json:"goal"`
	AcceptanceCriteria []task.AcceptanceCriterion `json:"acceptance_criteria"`
}

// StepInfo identifies the step in the run.
type StepInfo struct {
	Index int `json:"index"`
}

// RequestPaths are absolute paths for agent execution.
type RequestPaths struct {
	WorkspaceDir string `json:"workspace_dir"`
}

// RawAgentResponse is the response with json.RawMessage fields used by role MapResponse implementations.
type RawAgentResponse struct {
	Status     string `json:"status"`
	StopReason string `json:"stop_reason,omitempty"`
	Summary    string `json:"summary"`

	PlanOutput  json.RawMessage `json:"plan_output,omitempty"`
	DoOutput    json.RawMessage `json:"do_output,omitempty"`
	CheckOutput json.RawMessage `json:"check_output,omitempty"`
	ActOutput   json.RawMessage `json:"act_output,omitempty"`
}

// TaskState is ephemeral ADK session state for role handoff within one live run.
// Each role reads/writes its own output field.
type TaskState struct {
	Plan  json.RawMessage `json:"plan,omitempty"`
	Do    json.RawMessage `json:"do,omitempty"`
	Check json.RawMessage `json:"check,omitempty"`
	Act   json.RawMessage `json:"act,omitempty"`
}
