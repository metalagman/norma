// Package config provides configuration loading and management for norma.
package config

// Config is the root configuration.
type Config struct {
	Agents    map[string]AgentConfig `json:"agents"    mapstructure:"agents"`
	Budgets   Budgets                `json:"budgets"   mapstructure:"budgets"`
	Retention RetentionPolicy        `json:"retention" mapstructure:"retention"`
}

// AgentConfig describes how to run an agent.
type AgentConfig struct {
	Type   string   `json:"type"              mapstructure:"type"`
	Cmd    []string `json:"cmd,omitempty"     mapstructure:"cmd"`
	Model  string   `json:"model,omitempty"   mapstructure:"model"`
	Path   string   `json:"path,omitempty"    mapstructure:"path"`
	UseTTY *bool    `json:"use_tty,omitempty" mapstructure:"use_tty"`
}

// Budgets defines run limits.
type Budgets struct {
	MaxIterations   int `json:"max_iterations"              mapstructure:"max_iterations"`
	MaxPatchKB      int `json:"max_patch_kb,omitempty"      mapstructure:"max_patch_kb"`
	MaxChangedFiles int `json:"max_changed_files,omitempty" mapstructure:"max_changed_files"`
	MaxRiskyFiles   int `json:"max_risky_files,omitempty"   mapstructure:"max_risky_files"`
}

// RetentionPolicy defines how many old runs to keep.
type RetentionPolicy struct {
	KeepLast int `json:"keep_last,omitempty" mapstructure:"keep_last"`
	KeepDays int `json:"keep_days,omitempty" mapstructure:"keep_days"`
}
