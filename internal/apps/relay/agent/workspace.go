package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/internal/git"
	"github.com/rs/zerolog/log"
)

// WorkspaceManager manages git worktrees for relay sessions.
type WorkspaceManager struct {
	workingDir string
	baseBranch string
}

// NewWorkspaceManager creates a WorkspaceManager for the given working directory.
func NewWorkspaceManager(workingDir, baseBranch string) *WorkspaceManager {
	return &WorkspaceManager{
		workingDir: workingDir,
		baseBranch: strings.TrimSpace(baseBranch),
	}
}

// EnsureWorkspace creates or returns an existing workspace directory.
// If existingPath is non-empty and the directory exists, it is reused and synced with base.
// Otherwise a new worktree is mounted at workspacesDir/<key> using branch <branchName>.
func (m *WorkspaceManager) EnsureWorkspace(ctx context.Context, key, branchName, existingPath string) (string, error) {
	relayDir := filepath.Join(m.workingDir, ".norma")
	workspacesDir := filepath.Join(relayDir, "relay-sessions")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		return "", fmt.Errorf("create workspaces dir: %w", err)
	}

	workspaceDir := existingPath
	if strings.TrimSpace(workspaceDir) == "" {
		workspaceDir = filepath.Join(workspacesDir, key)
	}

	if fi, err := os.Stat(workspaceDir); err == nil && fi.IsDir() {
		// Workspace already exists — import latest base
		if err := m.Import(ctx, workspaceDir); err != nil {
			return "", fmt.Errorf("import base: %w", err)
		}
		return workspaceDir, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat workspace dir %q: %w", workspaceDir, err)
	}

	baseBranch, err := m.resolvedBaseBranch(ctx)
	if err != nil {
		return "", err
	}

	if _, err := git.MountWorktree(ctx, m.workingDir, workspaceDir, branchName, baseBranch); err != nil {
		return "", fmt.Errorf("mount worktree: %w", err)
	}

	return workspaceDir, nil
}

// Import syncs a workspace branch onto the configured base branch.
func (m *WorkspaceManager) Import(ctx context.Context, workspaceDir string) error {
	statusOut, err := git.GitRunCmdOutput(ctx, workspaceDir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read workspace status: %w", err)
	}

	status := strings.TrimSpace(statusOut)
	if status != "" {
		changedEntries := strings.Count(status, "\n") + 1
		log.Warn().
			Str("workspace", workspaceDir).
			Int("changed_entries", changedEntries).
			Msg("discarding dirty workspace changes before import")

		if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "reset", "--hard"); err != nil {
			return fmt.Errorf("reset dirty workspace before import: %w", err)
		}
		if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "clean", "-fd"); err != nil {
			return fmt.Errorf("clean dirty workspace before import: %w", err)
		}
	}

	baseRef, err := m.resolvedBaseBranch(ctx)
	if err != nil {
		return err
	}

	if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "rebase", baseRef); err != nil {
		// Abort rebase on failure so workspace stays clean
		_ = git.GitRunCmdErr(ctx, workspaceDir, "git", "rebase", "--abort")
		return fmt.Errorf("rebase workspace onto %s: %w", baseRef, err)
	}
	log.Info().Str("workspace", workspaceDir).Str("base_ref", baseRef).Msg("workspace synced to base ref")
	return nil
}

func (m *WorkspaceManager) resolvedBaseBranch(ctx context.Context) (string, error) {
	if branch := strings.TrimSpace(m.baseBranch); branch != "" {
		return branch, nil
	}

	branch, err := git.CurrentBranch(ctx, m.workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace base branch: %w", err)
	}
	return branch, nil
}

// Export squash-merges workspace branch into the configured base branch and commits.
func (m *WorkspaceManager) Export(ctx context.Context, workspaceDir, branchName, commitMessage string) error {
	mainRepo := m.workingDir
	baseBranch, err := m.resolvedBaseBranch(ctx)
	if err != nil {
		return err
	}
	currentBranch, err := git.CurrentBranch(ctx, mainRepo)
	if err != nil {
		return fmt.Errorf("resolve current repository branch: %w", err)
	}
	if currentBranch != baseBranch {
		return fmt.Errorf("export requires repository branch %q, current branch is %q", baseBranch, currentBranch)
	}

	// Stash local changes in main repo if dirty
	dirty := strings.TrimSpace(git.GitRunCmd(ctx, mainRepo, "git", "status", "--porcelain"))
	stashed := dirty != ""
	if stashed {
		if err := git.GitRunCmdErr(ctx, mainRepo, "git", "stash", "push", "-u", "-m", "norma pre-export"); err != nil {
			return fmt.Errorf("git stash push: %w", err)
		}
	}

	restoreStash := func() error {
		if !stashed {
			return nil
		}
		return git.GitRunCmdErr(ctx, mainRepo, "git", "stash", "pop")
	}

	beforeHash := strings.TrimSpace(git.GitRunCmd(ctx, mainRepo, "git", "rev-parse", "HEAD"))

	// Squash merge workspace branch into configured base branch.
	if err := git.GitRunCmdErr(ctx, mainRepo, "git", "merge", "--squash", branchName); err != nil {
		_ = git.GitRunCmdErr(ctx, mainRepo, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git merge --squash %s: %w", branchName, err)
	}

	// Stage and check if there are changes
	if err := git.GitRunCmdErr(ctx, mainRepo, "git", "add", "-A"); err != nil {
		_ = git.GitRunCmdErr(ctx, mainRepo, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git add: %w", err)
	}

	status := strings.TrimSpace(git.GitRunCmd(ctx, mainRepo, "git", "status", "--porcelain"))
	if status == "" {
		_ = restoreStash()
		log.Info().Str("base_branch", baseBranch).Msg("nothing to export — workspace already matches base branch")
		return nil
	}

	// Commit on configured base branch.
	if err := git.GitRunCmdErr(ctx, mainRepo, "git", "commit", "-m", commitMessage); err != nil {
		_ = git.GitRunCmdErr(ctx, mainRepo, "git", "reset", "--hard", beforeHash)
		_ = restoreStash()
		return fmt.Errorf("git commit: %w", err)
	}

	if err := restoreStash(); err != nil {
		return fmt.Errorf("git stash pop: %w", err)
	}

	afterHash := strings.TrimSpace(git.GitRunCmd(ctx, mainRepo, "git", "rev-parse", "HEAD"))
	log.Info().
		Str("branch", branchName).
		Str("base_branch", baseBranch).
		Str("before_hash", beforeHash).
		Str("after_hash", afterHash).
		Msg("workspace exported to base branch")

	return nil
}

// CleanupWorkspace removes a git worktree.
func (m *WorkspaceManager) CleanupWorkspace(ctx context.Context, workspaceDir string) error {
	if workspaceDir == "" {
		return nil
	}
	if err := git.RemoveWorktree(ctx, m.workingDir, workspaceDir); err != nil {
		log.Warn().Err(err).Str("workspace", workspaceDir).Msg("failed to remove worktree")
		return err
	}
	return nil
}
