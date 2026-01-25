package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds agent configuration and run budgets.
type Config struct {
	Agents  map[string]AgentConfig `json:"agents" mapstructure:"agents"`
	Budgets Budgets                `json:"budgets" mapstructure:"budgets"`
}

// AgentConfig describes how to invoke an agent.
type AgentConfig struct {
	Type   string   `json:"type" mapstructure:"type"`
	Cmd    []string `json:"cmd" mapstructure:"cmd"`
	Model  string   `json:"model" mapstructure:"model"`
	Path   string   `json:"path" mapstructure:"path"`
	UseTTY *bool    `json:"tty,omitempty" mapstructure:"tty"`
}

// Budgets mirror model budgets for config loading.
type Budgets struct {
	MaxIterations   int `json:"max_iterations" mapstructure:"max_iterations"`
	MaxPatchKB      int `json:"max_patch_kb,omitempty" mapstructure:"max_patch_kb"`
	MaxChangedFiles int `json:"max_changed_files,omitempty" mapstructure:"max_changed_files"`
	MaxRiskyFiles   int `json:"max_risky_files,omitempty" mapstructure:"max_risky_files"`
}

// Load reads config from the given path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Budgets.MaxIterations <= 0 {
		return Config{}, fmt.Errorf("budgets.max_iterations must be > 0")
	}
	return cfg, nil
}
