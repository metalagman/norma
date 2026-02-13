// Package main provides the entry point for the norma CLI.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/metalagman/norma/internal/git"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize norma in the current repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}

			if !git.Available(cmd.Context(), repoRoot) {
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

			gitignorePath := filepath.Join(normaDir, ".gitignore")
			if _, err := os.Stat(gitignorePath); err == nil {
				log.Info().Msg(".norma/.gitignore already exists, skipping")
			} else {
				log.Info().Str("path", gitignorePath).Msg("installing .norma/.gitignore")
				if err := os.WriteFile(gitignorePath, []byte(normaGitignoreContent), 0o644); err != nil {
					return fmt.Errorf("write .norma/.gitignore: %w", err)
				}
			}

			log.Info().Msg("initializing beads")
			if err := initBeads(); err != nil {
				return fmt.Errorf("init beads: %w", err)
			}

			configPath := filepath.Join(normaDir, "config.yaml")
			if _, err := os.Stat(configPath); err == nil {
				log.Info().Msg("config.yaml already exists, skipping")
			} else {
				log.Info().Str("path", configPath).Msg("installing default config")
				if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0o644); err != nil {
					return fmt.Errorf("write default config: %w", err)
				}
			}

			fmt.Println("norma initialized successfully")
			return nil
		},
	}
	return cmd
}

const normaGitignoreContent = `# ignore everything in .norma by default
*

# but keep this file itself
!.gitignore

# keep config
!config.yaml
!config.yml
`

const defaultConfigYAML = `profile: default
agents:
  codex_primary:
    type: codex
    model: gpt-5.2-codex
  codex_fast:
    type: codex
    model: gpt-5.1-codex-mini
  gemini_flash:
    type: gemini
    model: gemini-3-flash-preview
  opencode_exec_agent:
    type: opencode
    model: opencode/big-pickle

profiles:
  default:
    pdca:
      plan: codex_primary
      do: gemini_flash
      check: codex_primary
      act: codex_primary
    features:
      backlog_refiner:
        agents:
          planner: codex_primary
          implementer: gemini_flash
  codex:
    pdca:
      plan: codex_primary
      do: codex_fast
      check: codex_fast
      act: codex_primary
  gemini:
    pdca:
      plan: gemini_flash
      do: gemini_flash
      check: gemini_flash
      act: gemini_flash
  opencode:
    pdca:
      plan: opencode_exec_agent
      do: opencode_exec_agent
      check: opencode_exec_agent
      act: opencode_exec_agent
budgets:
  max_iterations: 5
retention:
  keep_last: 50
  keep_days: 30
`
