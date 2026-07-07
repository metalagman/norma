package config

import (
	"fmt"
	"strings"

	"github.com/normahq/runtime/v2/appconfig"
)

// CoreConfigFileName is the fallback config filename.
const CoreConfigFileName = appconfig.CoreConfigFileName

// RuntimeLoadOptions configures runtime config loading.
type RuntimeLoadOptions = appconfig.RuntimeLoadOptions

// CLISettings are app-specific settings for norma CLI commands.
type CLISettings struct {
	PDCA      PDCAAgentRefs   `mapstructure:"pdca"    validate:"required"`
	Budgets   Budgets         `mapstructure:"budgets"`
	Retention RetentionPolicy `mapstructure:"retention"`
}

// EffectiveBudgets returns budgets with defaults.
func (c CLISettings) EffectiveBudgets() Budgets {
	if c.Budgets.MaxIterations <= 0 {
		return Budgets{MaxIterations: 5}
	}
	return c.Budgets
}

// EffectiveRetention returns retention with defaults.
func (c CLISettings) EffectiveRetention() RetentionPolicy {
	if c.Retention.KeepLast <= 0 && c.Retention.KeepDays <= 0 {
		return RetentionPolicy{KeepLast: 50, KeepDays: 30}
	}
	return c.Retention
}

type cliConfigDocument struct {
	Runtime appconfig.RuntimeConfig `mapstructure:"runtime"`
	CLI     CLISettings             `mapstructure:"cli"`
}

type swarmConfigDocument struct {
	Runtime appconfig.RuntimeConfig `mapstructure:"runtime"`
	Swarm   SwarmSettings           `mapstructure:"swarm"`
}

type plannerConfigDocument struct {
	Runtime appconfig.RuntimeConfig `mapstructure:"runtime"`
	Planner PlannerSettings         `mapstructure:"planner"`
}

// LoadRuntime loads and validates runtime core config for norma CLI commands.
func LoadRuntime(opts RuntimeLoadOptions) (Config, error) {
	cfg, _, err := LoadRuntimeAndCLIConfig(opts)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadRuntimeAndCLIConfigUnresolved loads runtime config and CLI settings without resolving PDCA role IDs.
func LoadRuntimeAndCLIConfigUnresolved(opts RuntimeLoadOptions) (Config, CLISettings, error) {
	var doc cliConfigDocument
	selectedProfile, err := appconfig.LoadConfigDocument(opts, appconfig.AppLoadOptions{AppName: "cli"}, &doc)
	if err != nil {
		return Config{}, CLISettings{}, err
	}

	cfg := Config{
		Runtime: doc.Runtime,
		Profile: strings.TrimSpace(selectedProfile),
	}
	if cfg.Profile == "" {
		cfg.Profile = defaultProfile
	}

	return cfg, doc.CLI, nil
}

// LoadRuntimeAndCLIConfig loads runtime config and CLI app settings.
func LoadRuntimeAndCLIConfig(opts RuntimeLoadOptions) (Config, CLISettings, error) {
	cfg, cli, err := LoadRuntimeAndCLIConfigUnresolved(opts)
	if err != nil {
		return Config{}, CLISettings{}, err
	}

	roleIDs, err := cfg.ResolveRoleIDs(cli)
	if err != nil {
		return Config{}, CLISettings{}, fmt.Errorf("resolve role ids: %w", err)
	}
	cfg.RoleIDs = roleIDs

	return cfg, cli, nil
}

// LoadRuntimeAndSwarmConfig loads runtime config and swarm settings.
func LoadRuntimeAndSwarmConfig(opts RuntimeLoadOptions) (Config, SwarmSettings, error) {
	var doc swarmConfigDocument
	selectedProfile, err := appconfig.LoadConfigDocument(opts, appconfig.AppLoadOptions{AppName: "swarm"}, &doc)
	if err != nil {
		return Config{}, SwarmSettings{}, err
	}

	cfg := Config{
		Runtime: doc.Runtime,
		Profile: strings.TrimSpace(selectedProfile),
	}
	if cfg.Profile == "" {
		cfg.Profile = defaultProfile
	}

	return cfg, doc.Swarm, nil
}

// LoadRuntimeAndPlannerConfig loads runtime config and planner settings.
func LoadRuntimeAndPlannerConfig(opts RuntimeLoadOptions) (Config, PlannerSettings, error) {
	var doc plannerConfigDocument
	selectedProfile, err := appconfig.LoadConfigDocument(opts, appconfig.AppLoadOptions{AppName: "planner"}, &doc)
	if err != nil {
		return Config{}, PlannerSettings{}, err
	}

	cfg := Config{
		Runtime: doc.Runtime,
		Profile: strings.TrimSpace(selectedProfile),
	}
	if cfg.Profile == "" {
		cfg.Profile = defaultProfile
	}

	return cfg, doc.Planner, nil
}
