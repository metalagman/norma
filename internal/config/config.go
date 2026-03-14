// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/adk/agentconfig"
)

// Config is the root configuration.
type Config struct {
	Agents    map[string]agentconfig.Config `json:"agents,omitempty"   mapstructure:"agents"`
	Profiles  map[string]ProfileConfig      `json:"profiles,omitempty" mapstructure:"profiles"`
	Profile   string                        `json:"profile,omitempty"  mapstructure:"profile"`
	RoleIDs   map[string]string             `json:"-"                  mapstructure:"-"`
	Budgets   Budgets                       `json:"budgets"            mapstructure:"budgets"`
	Retention RetentionPolicy               `json:"retention"          mapstructure:"retention"`
}

// AgentConfig describes how to run an agent.
type AgentConfig = agentconfig.Config

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
	AgentTypeGenericACP = agentconfig.AgentTypeGenericACP

	AgentTypeCodexACP    = agentconfig.AgentTypeCodexACP
	AgentTypeOpenCodeACP = agentconfig.AgentTypeOpenCodeACP
	AgentTypeGeminiACP   = agentconfig.AgentTypeGeminiACP
	AgentTypeCopilotACP  = agentconfig.AgentTypeCopilotACP
)

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	return agentconfig.IsACPType(agentType)
}

// IsPlannerSupportedType reports whether planner mode supports the agent type.
func IsPlannerSupportedType(agentType string) bool {
	return agentconfig.IsPlannerSupportedType(agentType)
}

// ResolveAgentIDs returns the agent IDs for each role in the selected profile.
func (c Config) ResolveAgentIDs(profile string) (string, map[string]string, error) {
	if len(c.Agents) == 0 {
		return "", nil, fmt.Errorf("missing global agents configuration")
	}

	selected, profileCfg, err := c.resolveProfile(profile)
	if err != nil {
		return "", nil, err
	}

	refs := profileCfg.PDCA
	resolved := make(map[string]string, 5)

	resolve := func(role, agentName string) error {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return fmt.Errorf("profile %q missing %s agent reference", selected, role)
		}
		if _, exists := c.Agents[name]; !exists {
			return fmt.Errorf("profile %q references undefined agent %q in %s", selected, name, role)
		}
		resolved[role] = name
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
