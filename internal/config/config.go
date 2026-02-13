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
	APIKeyEnv     string        `json:"api_key_env,omitempty"    mapstructure:"api_key_env"`
	APIKey        string        `json:"api_key,omitempty"        mapstructure:"api_key"`
	Timeout       int           `json:"timeout,omitempty"        mapstructure:"timeout"`
	Path          string        `json:"path,omitempty"           mapstructure:"path"`
	UseTTY        *bool         `json:"use_tty,omitempty"        mapstructure:"use_tty"`
	MaxIterations int           `json:"max_iterations,omitempty" mapstructure:"max_iterations"`
	SubAgents     []AgentConfig `json:"sub_agents,omitempty"     mapstructure:"sub_agents"`
}

// ProfileConfig describes an agent profile.
type ProfileConfig struct {
	PDCA     PDCAAgentRefs            `json:"pdca,omitempty"     mapstructure:"pdca"`
	Features map[string]FeatureConfig `json:"features,omitempty" mapstructure:"features"`
}

// PDCAAgentRefs maps fixed PDCA roles to global agent names.
type PDCAAgentRefs struct {
	Plan  string `json:"plan,omitempty"  mapstructure:"plan"`
	Do    string `json:"do,omitempty"    mapstructure:"do"`
	Check string `json:"check,omitempty" mapstructure:"check"`
	Act   string `json:"act,omitempty"   mapstructure:"act"`
}

// FeatureConfig stores agent references used by non-PDCA features.
type FeatureConfig struct {
	Agents map[string]string `json:"agents,omitempty" mapstructure:"agents"`
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
	resolved := make(map[string]AgentConfig, 4)

	resolve := func(role, agentName string) error {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return fmt.Errorf("profile %q missing pdca.%s agent reference", selected, role)
		}
		agentCfg, exists := c.Agents[name]
		if !exists {
			return fmt.Errorf("profile %q references undefined agent %q in pdca.%s", selected, name, role)
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
	if err := validateFeatureRefs(selected, profileCfg.Features, c.Agents); err != nil {
		return "", nil, err
	}

	return selected, resolved, nil
}

// ResolveFeatureAgents returns resolved agent configs for a profile feature.
func (c Config) ResolveFeatureAgents(profile, feature string) (string, map[string]AgentConfig, error) {
	if len(c.Agents) == 0 {
		return "", nil, fmt.Errorf("missing global agents configuration")
	}

	selected, profileCfg, err := c.resolveProfile(profile)
	if err != nil {
		return "", nil, err
	}

	if err := validateFeatureRefs(selected, profileCfg.Features, c.Agents); err != nil {
		return "", nil, err
	}

	featureName := strings.TrimSpace(feature)
	if featureName == "" {
		return "", nil, fmt.Errorf("feature name is required")
	}

	featureCfg, ok := profileCfg.Features[featureName]
	if !ok {
		return "", nil, fmt.Errorf("profile %q feature %q not found", selected, featureName)
	}
	if len(featureCfg.Agents) == 0 {
		return "", nil, fmt.Errorf("profile %q feature %q has no agent references", selected, featureName)
	}

	resolved := make(map[string]AgentConfig, len(featureCfg.Agents))
	for refName, agentName := range featureCfg.Agents {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return "", nil, fmt.Errorf(
				"profile %q feature %q has empty agent reference for key %q",
				selected,
				featureName,
				refName,
			)
		}
		agentCfg, exists := c.Agents[name]
		if !exists {
			return "", nil, fmt.Errorf(
				"profile %q feature %q references undefined agent %q in agents.%s",
				selected,
				featureName,
				name,
				refName,
			)
		}
		resolved[refName] = agentCfg
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

func validateFeatureRefs(profile string, features map[string]FeatureConfig, registry map[string]AgentConfig) error {
	for featureName, featureCfg := range features {
		for refName, agentName := range featureCfg.Agents {
			name := strings.TrimSpace(agentName)
			if name == "" {
				return fmt.Errorf(
					"profile %q feature %q has empty agent reference for key %q",
					profile,
					featureName,
					refName,
				)
			}
			if _, ok := registry[name]; !ok {
				return fmt.Errorf(
					"profile %q feature %q references undefined agent %q in agents.%s",
					profile,
					featureName,
					name,
					refName,
				)
			}
		}
	}
	return nil
}
