package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/metalagman/norma/internal/git"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var runBeadsInit = func(ctx context.Context, repoRoot string) error {
	cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", "norma")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Command builds the `norma init` command.
func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize norma in the current repository",
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return err
			}
			if !git.Available(cmd.Context(), repoRoot) {
				return fmt.Errorf("current directory is not a git repository")
			}

			normaDir := filepath.Join(repoRoot, ".norma")
			log.Info().Str("dir", normaDir).Msg("creating norma directory")
			if err := os.MkdirAll(filepath.Join(normaDir, "runs"), 0o700); err != nil {
				return fmt.Errorf("create runs dir: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(normaDir, "locks"), 0o700); err != nil {
				return fmt.Errorf("create locks dir: %w", err)
			}

			gitignorePath := filepath.Join(normaDir, ".gitignore")
			if _, err := os.Stat(gitignorePath); err == nil {
				log.Info().Msg(".norma/.gitignore already exists, skipping")
			} else {
				log.Info().Str("path", gitignorePath).Msg("installing .norma/.gitignore")
				if err := os.WriteFile(gitignorePath, []byte(NormaGitignoreContent), 0o600); err != nil {
					return fmt.Errorf("write .norma/.gitignore: %w", err)
				}
			}

			log.Info().Msg("initializing beads")
			if err := initBeads(cmd.Context()); err != nil {
				return fmt.Errorf("init beads: %w", err)
			}

			configPath := filepath.Join(normaDir, "config.yaml")
			if _, err := os.Stat(configPath); err == nil {
				log.Info().Msg("config.yaml already exists, skipping")
			} else {
				log.Info().Str("path", configPath).Msg("installing default config")
				if err := os.WriteFile(configPath, []byte(DefaultConfigYAML), 0o600); err != nil {
					return fmt.Errorf("write default config: %w", err)
				}
			}

			fmt.Println("norma initialized successfully")
			return nil
		},
	}
}

func initBeads(ctx context.Context) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %w", err)
	}

	topLevelOut, err := git.GitRunCmdOutput(ctx, repoRoot, "git", "rev-parse", "--show-toplevel")
	if err == nil {
		repoRoot = strings.TrimSpace(topLevelOut)
	}

	beadsPath := filepath.Join(repoRoot, ".beads")
	if _, err := os.Stat(beadsPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat beads dir %q: %w", beadsPath, err)
	}

	log.Info().Str("path", beadsPath).Msg(".beads not found, initializing with prefix 'norma'")
	return runBeadsInit(ctx, repoRoot)
}

const NormaGitignoreContent = `# ignore everything in .norma by default
*

# but keep this file itself
!.gitignore

# keep config
!config.yaml
!config.yml
`

const DefaultConfigYAML = `profile: default
agents:
  gemini_acp_agent:
    type: gemini_acp
    model: gemini-3-flash-preview
  opencode_acp_agent:
    type: opencode_acp
    model: opencode/big-pickle
  codex_acp_agent:
    type: codex_acp
  custom_generic_acp_agent:
    type: generic_acp
    cmd: ["custom-acp-cli", "--acp"]

profiles:
  default:
    pdca:
      plan: gemini_acp_agent
      do: gemini_acp_agent
      check: gemini_acp_agent
      act: gemini_acp_agent
    planner: gemini_acp_agent
  gemini:
    pdca:
      plan: gemini_acp_agent
      do: gemini_acp_agent
      check: gemini_acp_agent
      act: gemini_acp_agent
    planner: gemini_acp_agent
  opencode:
    pdca:
      plan: opencode_acp_agent
      do: opencode_acp_agent
      check: opencode_acp_agent
      act: opencode_acp_agent
    planner: opencode_acp_agent
  acp:
    pdca:
      plan: gemini_acp_agent
      do: opencode_acp_agent
      check: codex_acp_agent
      act: codex_acp_agent
    planner: gemini_acp_agent
budgets:
  max_iterations: 5
retention:
  keep_last: 50
  keep_days: 30
`
