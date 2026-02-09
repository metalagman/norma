package main

import (
	"fmt"
	"path/filepath"

	"github.com/metalagman/norma/internal/config"
	"github.com/spf13/viper"
)

func loadConfig(repoRoot string) (config.Config, error) {
	path := viper.GetString("config")
	if path == "" {
		path = filepath.Join(".norma", "config.json")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	viper.SetConfigFile(path)
	viper.SetConfigType("json")
	if err := viper.ReadInConfig(); err != nil {
		return config.Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config.Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return config.Config{}, fmt.Errorf("parse config: %w", err)
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
