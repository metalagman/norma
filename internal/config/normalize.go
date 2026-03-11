package config

import (
	"fmt"

	"github.com/metalagman/norma/internal/adk/agentconfig"
)

// NormalizeAgentAliases canonicalizes alias agent types in config to generic runtimes.
func NormalizeAgentAliases(cfg Config, executablePath string) (Config, error) {
	normalizedAgents, err := agentconfig.NormalizeACPConfigs(cfg.Agents, executablePath)
	if err != nil {
		return Config{}, fmt.Errorf("normalize agent aliases: %w", err)
	}
	cfg.Agents = normalizedAgents
	return cfg, nil
}
