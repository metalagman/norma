// Package config provides configuration loading and management for norma.
package config

// Config is the root configuration.
type Config struct {
	Agents    map[string]AgentConfig `json:"agents"`
	Budgets   Budgets                `json:"budgets"`
	Retention RetentionPolicy        `json:"retention"`
}

// AgentConfig describes how to run an agent.
type AgentConfig struct {
	Type   string   `json:"type"`
	Cmd    []string `json:"cmd,omitempty"`
	Model  string   `json:"model,omitempty"`
	Path   string   `json:"path,omitempty"`
	UseTTY *bool    `json:"use_tty,omitempty"`
}

// Budgets defines run limits.
type Budgets struct {
	MaxIterations   int `json:"max_iterations"`
	MaxPatchKB      int `json:"max_patch_kb,omitempty"`
	MaxChangedFiles int `json:"max_changed_files,omitempty"`
	MaxRiskyFiles   int `json:"max_risky_files,omitempty"`
}

// RetentionPolicy defines how many old runs to keep.
type RetentionPolicy struct {
	KeepLast int `json:"keep_last,omitempty"`
	KeepDays int `json:"keep_days,omitempty"`
}
