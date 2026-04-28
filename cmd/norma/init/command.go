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

	"github.com/normahq/norma/internal/git"
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
    gemini_acp_agent:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
    opencode_acp_agent:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
    codex_acp_agent:
      type: codex_acp
      codex_acp:
        model: gpt-5-codex
    copilot_acp:
      type: copilot_acp
      copilot_acp:
        model: gpt-5-codex
    claude_code_acp_agent:
      type: claude_code_acp
      claude_code_acp:
        model: claude-sonnet-4-20250514
    custom_generic_acp_agent:
      type: generic_acp
      generic_acp:
        cmd: ["custom-acp-cli", "--acp"]
    fallback_pool:
      type: pool
      pool:
        members:
          - opencode_acp_agent
          - gemini_acp_agent

  # Example MCP server configurations:
  # mcp_servers:
  #   my_mcp_server:
  #     type: stdio
  #     cmd: ["npx", "-y", "@example/mcp-server"]

cli:
  pdca:
    plan: gemini_acp_agent
    do: gemini_acp_agent
    check: gemini_acp_agent
    act: gemini_acp_agent
  budgets:
    max_iterations: 5
  retention:
    keep_last: 50
    keep_days: 30
planner:
  provider: gemini_acp_agent
swarm:
  primary_role: coordinator
  default_provider: gemini_acp_agent
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
        plan: gemini_acp_agent
        do: gemini_acp_agent
        check: gemini_acp_agent
        act: gemini_acp_agent
    planner:
      provider: gemini_acp_agent
    swarm:
      default_provider: gemini_acp_agent
  gemini:
    cli:
      pdca:
        plan: gemini_acp_agent
        do: gemini_acp_agent
        check: gemini_acp_agent
        act: gemini_acp_agent
    planner:
      provider: gemini_acp_agent
    swarm:
      default_provider: gemini_acp_agent
  opencode:
    cli:
      pdca:
        plan: opencode_acp_agent
        do: opencode_acp_agent
        check: opencode_acp_agent
        act: opencode_acp_agent
    planner:
      provider: opencode_acp_agent
    swarm:
      default_provider: opencode_acp_agent
  codex:
    cli:
      pdca:
        plan: codex_acp_agent
        do: codex_acp_agent
        check: codex_acp_agent
        act: codex_acp_agent
    planner:
      provider: codex_acp_agent
    swarm:
      default_provider: codex_acp_agent
  acp:
    cli:
      pdca:
        plan: gemini_acp_agent
        do: opencode_acp_agent
        check: codex_acp_agent
        act: codex_acp_agent
    planner:
      provider: gemini_acp_agent
    swarm:
      default_provider: codex_acp_agent
  copilot:
    cli:
      pdca:
        plan: copilot_acp
        do: copilot_acp
        check: copilot_acp
        act: copilot_acp
    planner:
      provider: copilot_acp
    swarm:
      default_provider: copilot_acp
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
