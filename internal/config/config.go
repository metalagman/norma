// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"strings"

	"github.com/normahq/norma/pkg/runtime/agentconfig"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
)

// Config is the root configuration.
type Config struct {
	Norma   runtimeconfig.NormaConfig `json:"norma"            mapstructure:"norma"  validate:"required"`
	Profile string                    `json:"profile,omitempty" mapstructure:"profile"`
	RoleIDs map[string]string         `json:"-"                 mapstructure:"-"`
}

// Budgets defines run limits (optional, defaults to 5 iterations if not set).
type Budgets struct {
	MaxIterations int `json:"max_iterations,omitempty" mapstructure:"max_iterations" validate:"omitempty,min=1"`
}

// RetentionPolicy defines how many old runs to keep (optional).
type RetentionPolicy struct {
	KeepLast int `json:"keep_last,omitempty" mapstructure:"keep_last" validate:"omitempty,min=1"`
	KeepDays int `json:"keep_days,omitempty" mapstructure:"keep_days" validate:"omitempty,min=1"`
}

// AgentConfig describes how to run an agent.
type AgentConfig = agentconfig.Config

// MCPServerConfig describes an MCP server configuration.
type MCPServerConfig = agentconfig.MCPServerConfig

// PDCAAgentRefs maps fixed PDCA roles to global agent names.
type PDCAAgentRefs struct {
	Plan  string `json:"plan,omitempty"  mapstructure:"plan"  validate:"required,min=1"`
	Do    string `json:"do,omitempty"    mapstructure:"do"    validate:"required,min=1"`
	Check string `json:"check,omitempty" mapstructure:"check" validate:"required,min=1"`
	Act   string `json:"act,omitempty"   mapstructure:"act"   validate:"required,min=1"`
}

const defaultProfile = "default"

// Supported agent types.
const (
	AgentTypeGenericACP = agentconfig.AgentTypeGenericACP

	AgentTypeCodexACP      = agentconfig.AgentTypeCodexACP
	AgentTypeOpenCodeACP   = agentconfig.AgentTypeOpenCodeACP
	AgentTypeGeminiACP     = agentconfig.AgentTypeGeminiACP
	AgentTypeCopilotACP    = agentconfig.AgentTypeCopilotACP
	AgentTypeClaudeCodeACP = agentconfig.AgentTypeClaudeCodeACP
)

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	return agentconfig.IsACPType(agentType)
}

// IsPlannerSupportedType reports whether planner mode supports the agent type.
func IsPlannerSupportedType(agentType string) bool {
	return agentconfig.IsPlannerSupportedType(agentType)
}

// ResolveRoleIDs resolves role agent IDs from CLI app settings.
func (c Config) ResolveRoleIDs(cli CLISettings) (map[string]string, error) {
	if len(c.Norma.Providers) == 0 {
		return nil, fmt.Errorf("missing global agents configuration")
	}

	refs := cli.PDCA
	resolved := make(map[string]string, 5)

	resolve := func(role, agentName string) error {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return fmt.Errorf("profile %q missing cli.%s agent reference", c.Profile, role)
		}
		if _, exists := c.Norma.Providers[name]; !exists {
			return fmt.Errorf("profile %q references undefined agent %q in cli.%s", c.Profile, name, role)
		}
		resolved[role] = name
		return nil
	}

	if err := resolve("plan", refs.Plan); err != nil {
		return nil, err
	}
	if err := resolve("do", refs.Do); err != nil {
		return nil, err
	}
	if err := resolve("check", refs.Check); err != nil {
		return nil, err
	}
	if err := resolve("act", refs.Act); err != nil {
		return nil, err
	}

	if cli.Planner != "" {
		if err := resolve("planner", cli.Planner); err != nil {
			return nil, err
		}
	}

	return resolved, nil
}
