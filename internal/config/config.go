// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/adk/modelfactory"
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
type AgentConfig = modelfactory.ModelConfig

// ToModelConfig converts AgentConfig to adk modelfactory.ModelConfig.
func ToModelConfig(c AgentConfig) modelfactory.ModelConfig {
	return c
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
	AgentTypeExec        = modelfactory.ModelTypeExec
	AgentTypeACPExec     = modelfactory.ModelTypeACPExec
	AgentTypeCodex       = modelfactory.ModelTypeCodex
	AgentTypeCodexACP    = modelfactory.ModelTypeCodexACP
	AgentTypeOpenCode    = modelfactory.ModelTypeOpenCode
	AgentTypeOpenCodeACP = modelfactory.ModelTypeOpenCodeACP
	AgentTypeGemini      = modelfactory.ModelTypeGemini
	AgentTypeGeminiACP   = modelfactory.ModelTypeGeminiACP
	AgentTypeClaude      = modelfactory.ModelTypeClaude
)

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeACPExec, AgentTypeGeminiACP, AgentTypeOpenCodeACP, AgentTypeCodexACP:
		return true
	default:
		return false
	}
}

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

// ResolveProfile returns the profile configuration for the given profile name.
func (c Config) ResolveProfile(profile string) (string, ProfileConfig, error) {
	return c.resolveProfile(profile)
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
