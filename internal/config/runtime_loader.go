package config

import (
	"fmt"
	"strings"

	"github.com/normahq/norma/pkg/runtime/appconfig"
)

// CoreConfigFileName is the fallback config filename.
const CoreConfigFileName = appconfig.CoreConfigFileName

// RuntimeLoadOptions configures runtime config loading.
type RuntimeLoadOptions = appconfig.RuntimeLoadOptions

// CLISettings are app-specific settings for norma CLI commands.
type CLISettings struct {
	PDCA      PDCAAgentRefs   `mapstructure:"pdca"    validate:"required"`
	Planner   string          `mapstructure:"planner" validate:"omitempty,min=1"`
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

// LoadRuntime loads and validates runtime core config for norma CLI commands.
func LoadRuntime(opts RuntimeLoadOptions) (Config, error) {
	cfg, _, err := LoadRuntimeAndCLIConfig(opts)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadRuntimeAndCLIConfig loads runtime config and CLI app settings.
func LoadRuntimeAndCLIConfig(opts RuntimeLoadOptions) (Config, CLISettings, error) {
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

	roleIDs, err := cfg.ResolveRoleIDs(doc.CLI)
	if err != nil {
		return Config{}, CLISettings{}, fmt.Errorf("resolve role ids: %w", err)
	}
	cfg.RoleIDs = roleIDs

	return cfg, doc.CLI, nil
}
