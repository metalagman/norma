// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"strings"
)

// Config is the root configuration.
type Config struct {
	Agents    map[string]AgentConfig   `json:"agents,omitempty"   mapstructure:"agents"`
	Profiles  map[string]ProfileConfig `json:"profiles,omitempty" mapstructure:"profiles"`
	Profile   string                   `json:"profile,omitempty"  mapstructure:"profile"`
	Budgets   Budgets                  `json:"budgets"            mapstructure:"budgets"`
	Retention RetentionPolicy          `json:"retention"          mapstructure:"retention"`
}

// AgentConfig describes how to run an agent.
type AgentConfig struct {
	Type   string   `json:"type"              mapstructure:"type"`
	Cmd    []string `json:"cmd,omitempty"     mapstructure:"cmd"`
	Model  string   `json:"model,omitempty"   mapstructure:"model"`
	Path   string   `json:"path,omitempty"    mapstructure:"path"`
	UseTTY *bool    `json:"use_tty,omitempty" mapstructure:"use_tty"`
}

// ProfileConfig describes an agent profile.
type ProfileConfig struct {
	Agents map[string]AgentConfig `json:"agents,omitempty" mapstructure:"agents"`
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

const defaultProfile = "default"

// ResolveAgents returns the agents for the selected profile.
func (c Config) ResolveAgents(profile string) (string, map[string]AgentConfig, error) {
	selected := strings.TrimSpace(profile)
	if selected == "" {
		selected = strings.TrimSpace(c.Profile)
	}

	if len(c.Profiles) == 0 {
		if selected != "" && selected != defaultProfile {
			return "", nil, fmt.Errorf("profile %q not found (no profiles configured)", selected)
		}
		if len(c.Agents) == 0 {
			return "", nil, fmt.Errorf("missing agents configuration")
		}
		if selected == "" {
			selected = defaultProfile
		}
		return selected, c.Agents, nil
	}

	if selected == "" {
		selected = defaultProfile
	}
	profileCfg, ok := c.Profiles[selected]
	if !ok {
		return "", nil, fmt.Errorf("profile %q not found", selected)
	}
	if len(profileCfg.Agents) == 0 {
		return "", nil, fmt.Errorf("profile %q missing agents configuration", selected)
	}
	return selected, profileCfg.Agents, nil
}
