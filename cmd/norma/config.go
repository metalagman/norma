package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/metalagman/norma/internal/config"
	"github.com/spf13/viper"
)

var defaultConfigPath = filepath.Join(".norma", "config.yaml")

func resolveConfigPath(repoRoot, configuredPath string) string {
	path := strings.TrimSpace(configuredPath)
	if path == "" {
		path = defaultConfigPath
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	return path
}

func loadConfig(repoRoot string) (config.Config, error) {
	cfg, err := loadRawConfig(repoRoot)
	if err != nil {
		return config.Config{}, err
	}
	selectedProfile, agents, err := cfg.ResolveAgents(viper.GetString("profile"))
	if err != nil {
		return config.Config{}, err
	}
	cfg.Profile = selectedProfile
	cfg.Agents = agents
	if cfg.Budgets.MaxIterations <= 0 {
		return config.Config{}, fmt.Errorf("budgets.max_iterations must be > 0")
	}
	return cfg, nil
}

func loadRawConfig(repoRoot string) (config.Config, error) {
	path := resolveConfigPath(repoRoot, viper.GetString("config"))
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return config.Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
