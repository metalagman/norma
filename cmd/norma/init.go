// Package main provides the entry point for the norma CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/metalagman/norma/internal/run"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new norma project",
		Long:  "Initialize a new norma project by creating .norma directory, initializing beads, and installing a default config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}

			if !run.GitAvailable(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			normaDir := filepath.Join(repoRoot, ".norma")
			log.Info().Str("dir", normaDir).Msg("creating norma directory")
			if err := os.MkdirAll(filepath.Join(normaDir, "runs"), 0o755); err != nil {
				return fmt.Errorf("create runs dir: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(normaDir, "locks"), 0o755); err != nil {
				return fmt.Errorf("create locks dir: %w", err)
			}

			log.Info().Msg("initializing beads")
			if err := initBeads(); err != nil {
				return fmt.Errorf("init beads: %w", err)
			}

			configPath := filepath.Join(normaDir, "config.json")
			if _, err := os.Stat(configPath); err == nil {
				log.Info().Msg("config.json already exists, skipping")
			} else {
				log.Info().Str("path", configPath).Msg("installing default config")
				defaultConfig := map[string]any{
					"profile": "default",
					"profiles": map[string]any{
						"default": map[string]any{
							"agents": map[string]any{
								"plan":  map[string]any{"type": "codex", "model": "gpt-5.2-codex"},
								"do":    map[string]any{"type": "gemini", "model": "gemini-3-flash-preview"},
								"check": map[string]any{"type": "codex", "model": "gpt-5.2-codex"},
								"act":   map[string]any{"type": "codex", "model": "gpt-5.2-codex"},
							},
						},
						"codex": map[string]any{
							"agents": map[string]any{
								"plan":  map[string]any{"type": "codex", "model": "gpt-5.2-codex"},
								"do":    map[string]any{"type": "codex", "model": "gpt-5.1-codex-mini"},
								"check": map[string]any{"type": "codex", "model": "gpt-5.1-codex-mini"},
								"act":   map[string]any{"type": "codex", "model": "gpt-5.2-codex"},
							},
						},
						"gemini": map[string]any{
							"agents": map[string]any{
								"plan":  map[string]any{"type": "gemini", "model": "gemini-3-flash-preview"},
								"do":    map[string]any{"type": "gemini", "model": "gemini-3-flash-preview"},
								"check": map[string]any{"type": "gemini", "model": "gemini-3-flash-preview"},
								"act":   map[string]any{"type": "gemini", "model": "gemini-3-flash-preview"},
							},
						},
						"opencode": map[string]any{
							"agents": map[string]any{
								"plan":  map[string]any{"type": "opencode", "model": "opencode/big-pickle"},
								"do":    map[string]any{"type": "opencode", "model": "opencode/big-pickle"},
								"check": map[string]any{"type": "opencode", "model": "opencode/big-pickle"},
								"act":   map[string]any{"type": "opencode", "model": "opencode/big-pickle"},
							},
						},
					},
					"budgets": map[string]any{
						"max_iterations": 5,
					},
					"retention": map[string]any{
						"keep_last": 50,
						"keep_days": 30,
					},
				}
				data, err := json.MarshalIndent(defaultConfig, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal default config: %w", err)
				}
				if err := os.WriteFile(configPath, data, 0o644); err != nil {
					return fmt.Errorf("write default config: %w", err)
				}
			}

			fmt.Println("norma initialized successfully")
			return nil
		},
	}
}
