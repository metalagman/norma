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
	Type          string        `json:"type"                     mapstructure:"type"`
	Cmd           []string      `json:"cmd,omitempty"            mapstructure:"cmd"`
	Model         string        `json:"model,omitempty"          mapstructure:"model"`
	BaseURL       string        `json:"base_url,omitempty"       mapstructure:"base_url"`
	APIKey        string        `json:"api_key,omitempty"        mapstructure:"api_key"`
	Timeout       int           `json:"timeout,omitempty"        mapstructure:"timeout"`
	Path          string        `json:"path,omitempty"           mapstructure:"path"`
	UseTTY        *bool         `json:"use_tty,omitempty"        mapstructure:"use_tty"`
	MaxIterations int           `json:"max_iterations,omitempty" mapstructure:"max_iterations"`
	SubAgents     []AgentConfig `json:"sub_agents,omitempty"     mapstructure:"sub_agents"`
}

// ProfileConfig describes an agent profile.
type ProfileConfig struct {
	PDCA    PDCAAgentRefs `json:"pdca,omitempty"    mapstructure:"pdca"`
	Planner string        `json:"planner,omitempty" mapstructure:"planner"`
}

// PDCAAgentRefs maps fixed PDCA roles to global agent names.
type PDCAAgentRefs struct {
	Plan  string `json:"plan,omitempty"  mapstructure:"plan"`
	Do    string `json:"do,omitempty"    mapstructure:"do"`
	Check string `json:"check,omitempty" mapstructure:"check"`
	Act   string `json:"act,omitempty"   mapstructure:"act"`
}

// Budgets defines run limits.
type Budgets struct {
	MaxIterations int `json:"max_iterations" mapstructure:"max_iterations"`
}

// RetentionPolicy defines how many old runs to keep.
type RetentionPolicy struct {
	KeepLast int `json:"keep_last,omitempty" mapstructure:"keep_last"`
	KeepDays int `json:"keep_days,omitempty" mapstructure:"keep_days"`
}

const defaultProfile = "default"

// Supported agent types.
const (
	AgentTypeExec     = "exec"
	AgentTypeCodex    = "codex"
	AgentTypeOpenCode = "opencode"
	AgentTypeGemini   = "gemini"
	AgentTypeClaude   = "claude"
	AgentTypeOpenAI   = "openai"
)

// ResolveAgents returns the agents for the selected profile.
func (c Config) ResolveAgents(profile string) (string, map[string]AgentConfig, error) {
	if len(c.Agents) == 0 {
		return "", nil, fmt.Errorf("missing global agents configuration")
	}

	selected, profileCfg, err := c.resolveProfile(profile)
	if err != nil {
		return "", nil, err
	}

	refs := profileCfg.PDCA
	resolved := make(map[string]AgentConfig, 5)

	resolve := func(role, agentName string) error {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return fmt.Errorf("profile %q missing %s agent reference", selected, role)
		}
		agentCfg, exists := c.Agents[name]
		if !exists {
			return fmt.Errorf("profile %q references undefined agent %q in %s", selected, name, role)
		}
		resolved[role] = agentCfg
		return nil
	}

	if err := resolve("plan", refs.Plan); err != nil {
		return "", nil, err
	}
	if err := resolve("do", refs.Do); err != nil {
		return "", nil, err
	}
	if err := resolve("check", refs.Check); err != nil {
		return "", nil, err
	}
	if err := resolve("act", refs.Act); err != nil {
		return "", nil, err
	}

	if profileCfg.Planner != "" {
		if err := resolve("planner", profileCfg.Planner); err != nil {
			return "", nil, err
		}
	}

	return selected, resolved, nil
}

func (c Config) resolveProfile(profile string) (string, ProfileConfig, error) {
	selected := strings.TrimSpace(profile)
	if selected == "" {
		selected = strings.TrimSpace(c.Profile)
	}
	if selected == "" {
		selected = defaultProfile
	}
	if len(c.Profiles) == 0 {
		return "", ProfileConfig{}, fmt.Errorf("missing profiles configuration")
	}

	profileCfg, ok := c.Profiles[selected]
	if !ok {
		return "", ProfileConfig{}, fmt.Errorf("profile %q not found", selected)
	}

	return selected, profileCfg, nil
}
