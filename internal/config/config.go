// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"strings"

	"github.com/normahq/runtime/agentconfig"
	runtimeconfig "github.com/normahq/runtime/appconfig"
)

// Config is the root configuration.
type Config struct {
	Runtime runtimeconfig.RuntimeConfig `json:"runtime"           mapstructure:"runtime"  validate:"required"`
	Profile string                      `json:"profile,omitempty" mapstructure:"profile"`
	RoleIDs map[string]string           `json:"-"                  mapstructure:"-"`
}

// PlannerSettings defines config for the interactive planner app.
type PlannerSettings struct {
	Provider string `json:"provider,omitempty" mapstructure:"provider" validate:"omitempty,min=1"`
}

// SwarmSettings defines config for assignee-routed swarm execution.
type SwarmSettings struct {
	PrimaryRole     string                     `json:"primary_role,omitempty"     mapstructure:"primary_role"     validate:"omitempty,min=1"`
	DefaultProvider string                     `json:"default_provider,omitempty" mapstructure:"default_provider" validate:"omitempty,min=1"`
	Roles           map[string]SwarmRoleConfig `json:"roles,omitempty"            mapstructure:"roles"`
}

// SwarmRoleConfig defines one swarm role worker.
type SwarmRoleConfig struct {
	Assignee    string `json:"assignee,omitempty"    mapstructure:"assignee"    validate:"omitempty,min=1"`
	Instruction string `json:"instruction,omitempty" mapstructure:"instruction" validate:"omitempty,min=1"`
	Provider    string `json:"provider,omitempty"    mapstructure:"provider"    validate:"omitempty,min=1"`
}

// ResolvedSwarmRoleConfig is a swarm role with its effective provider.
type ResolvedSwarmRoleConfig struct {
	Key           string
	Assignee      string
	Instruction   string
	ProviderID    string
	IsPrimaryRole bool
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

// ResolveRoleIDs resolves PDCA role agent IDs from CLI app settings.
func (c Config) ResolveRoleIDs(cli CLISettings) (map[string]string, error) {
	if len(c.Runtime.Providers) == 0 {
		return nil, fmt.Errorf("missing global agents configuration")
	}

	refs := cli.PDCA
	resolved := make(map[string]string, 4)

	resolve := func(role, agentName string) error {
		name := strings.TrimSpace(agentName)
		if name == "" {
			return fmt.Errorf("profile %q missing cli.%s agent reference", c.Profile, role)
		}
		if _, exists := c.Runtime.Providers[name]; !exists {
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

	return resolved, nil
}

// ResolvePlannerProvider validates and resolves planner provider settings.
func (c Config) ResolvePlannerProvider(planner PlannerSettings) (string, error) {
	if len(c.Runtime.Providers) == 0 {
		return "", fmt.Errorf("missing global agents configuration")
	}

	providerID := strings.TrimSpace(planner.Provider)
	if providerID == "" {
		return "", fmt.Errorf("planner.provider is required")
	}
	if _, ok := c.Runtime.Providers[providerID]; !ok {
		return "", fmt.Errorf("planner.provider %q is not defined in runtime.providers", providerID)
	}
	return providerID, nil
}

// ResolveSwarmRoles validates and resolves swarm roles from swarm settings.
func (c Config) ResolveSwarmRoles(swarm SwarmSettings) (map[string]ResolvedSwarmRoleConfig, error) {
	if len(c.Runtime.Providers) == 0 {
		return nil, fmt.Errorf("missing global agents configuration")
	}
	if len(swarm.Roles) == 0 {
		return nil, fmt.Errorf("swarm.roles is required")
	}

	primaryRole := strings.TrimSpace(swarm.PrimaryRole)
	if primaryRole == "" {
		return nil, fmt.Errorf("swarm.primary_role is required")
	}
	if _, ok := swarm.Roles[primaryRole]; !ok {
		return nil, fmt.Errorf("swarm.primary_role %q does not exist in swarm.roles", primaryRole)
	}

	defaultProvider := strings.TrimSpace(swarm.DefaultProvider)
	if defaultProvider == "" {
		return nil, fmt.Errorf("swarm.default_provider is required")
	}
	if _, ok := c.Runtime.Providers[defaultProvider]; !ok {
		return nil, fmt.Errorf("swarm.default_provider %q is not defined in runtime.providers", defaultProvider)
	}

	resolved := make(map[string]ResolvedSwarmRoleConfig, len(swarm.Roles))
	seenAssignees := make(map[string]string, len(swarm.Roles))
	for key, role := range swarm.Roles {
		roleKey := strings.TrimSpace(key)
		if roleKey == "" {
			return nil, fmt.Errorf("swarm.roles contains an empty role key")
		}
		assignee := strings.TrimSpace(role.Assignee)
		if assignee == "" {
			return nil, fmt.Errorf("swarm.roles.%s.assignee is required", roleKey)
		}
		instruction := strings.TrimSpace(role.Instruction)
		if instruction == "" {
			return nil, fmt.Errorf("swarm.roles.%s.instruction is required", roleKey)
		}
		providerID := strings.TrimSpace(role.Provider)
		if providerID == "" {
			providerID = defaultProvider
		}
		if _, ok := c.Runtime.Providers[providerID]; !ok {
			return nil, fmt.Errorf("swarm.roles.%s.provider %q is not defined in runtime.providers", roleKey, providerID)
		}
		if prev, exists := seenAssignees[assignee]; exists {
			return nil, fmt.Errorf("swarm.roles.%s.assignee duplicates swarm.roles.%s.assignee (%q)", roleKey, prev, assignee)
		}
		seenAssignees[assignee] = roleKey
		resolved[roleKey] = ResolvedSwarmRoleConfig{
			Key:           roleKey,
			Assignee:      assignee,
			Instruction:   instruction,
			ProviderID:    providerID,
			IsPrimaryRole: roleKey == primaryRole,
		}
	}

	return resolved, nil
}
