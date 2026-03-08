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
  codex_primary:
    type: codex
    model: gpt-5.2-codex
  codex_fast:
    type: codex
    model: gpt-5.1-codex-mini
  gemini_flash:
    type: gemini_aistudio
    model: gemini-3-flash-preview
    api_key: ${GOOGLE_API_KEY}
  claude_primary:
    type: claude
    model: claude-3-opus
  opencode_exec_model:
    type: opencode
    model: opencode/big-pickle
  gemini_acp_agent:
    type: gemini_acp
    model: gemini-3-flash-preview
  opencode_acp_agent:
    type: opencode_acp
    model: opencode/big-pickle
  codex_acp_agent:
    type: codex_acp
  custom_acp_exec_agent:
    type: acp_exec
    cmd: ["custom-acp-cli", "--acp"]

profiles:
  default:
    pdca:
      plan: codex_primary
      do: gemini_flash
      check: codex_primary
      act: codex_primary
    planner: codex_primary
  codex:
    pdca:
      plan: codex_primary
      do: codex_fast
      check: codex_fast
      act: codex_primary
    planner: codex_primary
  claude:
    pdca:
      plan: claude_primary
      do: claude_primary
      check: claude_primary
      act: claude_primary
    planner: claude_primary
  gemini:
    pdca:
      plan: gemini_flash
      do: gemini_flash
      check: gemini_flash
      act: gemini_flash
    planner: gemini_flash
  opencode:
    pdca:
      plan: opencode_exec_model
      do: opencode_exec_model
      check: opencode_exec_model
      act: opencode_exec_model
    planner: opencode_exec_model
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
