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

	"github.com/normahq/norma/v2/internal/git"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var runBeadsInit = func(ctx context.Context, workingDir string) error {
	cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", "norma")
	cmd.Dir = workingDir
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
			workingDir, err := os.Getwd()
			if err != nil {
				return err
			}
			if !git.Available(cmd.Context(), workingDir) {
				return fmt.Errorf("current directory is not a git repository")
			}

			normaDir := filepath.Join(workingDir, ".norma")
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
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %w", err)
	}

	topLevelOut, err := git.GitRunCmdOutput(ctx, workingDir, "git", "rev-parse", "--show-toplevel")
	if err == nil {
		workingDir = strings.TrimSpace(topLevelOut)
	}

	beadsPath := filepath.Join(workingDir, ".beads")
	if _, err := os.Stat(beadsPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat beads dir %q: %w", beadsPath, err)
	}

	log.Info().Str("path", beadsPath).Msg(".beads not found, initializing with prefix 'norma'")
	return runBeadsInit(ctx, workingDir)
}

const NormaGitignoreContent = `# ignore everything in .norma by default
*

# but keep this file itself
!.gitignore

# keep config
!config.yaml
`
const DefaultConfigYAML = `runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
    codex:
      type: codex_acp
      codex_acp:
        model: gpt-5-codex
    claude_code:
      type: claude_code_acp
      claude_code_acp:
        model: claude-sonnet-4-20250514
    copilot:
      type: copilot_acp
      copilot_acp:
        model: gpt-5-codex
    gemini:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
    custom_generic:
      type: generic_acp
      generic_acp:
        cmd: ["custom-acp-cli", "--acp"]
    fallback_pool:
      type: pool
      pool:
        members:
          - opencode
          - codex

  # Example MCP server configurations:
  # mcp_servers:
  #   my_mcp_server:
  #     type: stdio
  #     cmd: ["npx", "-y", "@example/mcp-server"]

cli:
  pdca:
    plan: opencode
    do: opencode
    check: opencode
    act: opencode
  budgets:
    max_iterations: 5
  retention:
    keep_last: 50
    keep_days: 30
planner:
  provider: opencode
swarm:
  primary_role: coordinator
  default_provider: opencode
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: Decide routing, resolve bounced tasks, supervise swarm progress.
    planner:
      assignee: norma-planner
      instruction: Break down work and assign tasks to roles.
    implementer:
      assignee: norma-implementer
      instruction: Implement assigned tasks.
profiles:
  default:
    cli:
      pdca:
        plan: opencode
        do: opencode
        check: opencode
        act: opencode
    planner:
      provider: opencode
    swarm:
      default_provider: opencode
  opencode:
    cli:
      pdca:
        plan: opencode
        do: opencode
        check: opencode
        act: opencode
    planner:
      provider: opencode
    swarm:
      default_provider: opencode
  codex:
    cli:
      pdca:
        plan: codex
        do: codex
        check: codex
        act: codex
    planner:
      provider: codex
    swarm:
      default_provider: codex
  claude_code:
    cli:
      pdca:
        plan: claude_code
        do: claude_code
        check: claude_code
        act: claude_code
    planner:
      provider: claude_code
    swarm:
      default_provider: claude_code
  copilot:
    cli:
      pdca:
        plan: copilot
        do: copilot
        check: copilot
        act: copilot
    planner:
      provider: copilot
    swarm:
      default_provider: copilot
  gemini:
    cli:
      pdca:
        plan: gemini
        do: gemini
        check: gemini
        act: gemini
    planner:
      provider: gemini
    swarm:
      default_provider: gemini
  pool_fallback:
    cli:
      pdca:
        plan: fallback_pool
        do: fallback_pool
        check: fallback_pool
        act: fallback_pool
    planner:
      provider: fallback_pool
    swarm:
      default_provider: fallback_pool
`
