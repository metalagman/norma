// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

func createWorkspace(ctx context.Context, repoRoot, runDir, taskID string) (string, error) {
	workspaceDir := filepath.Join(runDir, "workspace")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}

	// We create a branch scoped to the task to allow restartable progress.
	branchName := fmt.Sprintf("norma/task/%s", taskID)

	log.Info().
		Str("repo_root", repoRoot).
		Str("workspace_dir", workspaceDir).
		Str("branch", branchName).
		Msg("creating git workspace")

	// Check if we are in a git repo
	if !gitAvailable(ctx, repoRoot) {
		return "", fmt.Errorf("not a git repository: %s", repoRoot)
	}

	// Prune any stale worktree metadata
	_ = runCmdErr(ctx, repoRoot, "git", "worktree", "prune")

	// Check if branch already exists
	branchExists := strings.TrimSpace(runCmd(ctx, repoRoot, "git", "branch", "--list", branchName)) != ""

	if branchExists {
		// Ensure it's not checked out in another worktree
		forceCleanupStaleWorktree(ctx, repoRoot, branchName)
	}

	args := []string{"worktree", "add", "-b", branchName, workspaceDir}
	if branchExists {
		args = []string{"worktree", "add", workspaceDir, branchName}
	}

	// Create worktree
	err := runCmdErr(ctx, repoRoot, "git", args...)
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return workspaceDir, nil
}

func forceCleanupStaleWorktree(ctx context.Context, repoRoot, branchName string) {
	out := runCmd(ctx, repoRoot, "git", "worktree", "list", "--porcelain")
	lines := strings.Split(out, "\n")
	var currentWorktree string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currentWorktree = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch ") {
			branch := strings.TrimPrefix(line, "branch refs/heads/")
			if branch == branchName {
				log.Warn().Str("branch", branchName).Str("stale_worktree", currentWorktree).Msg("found stale worktree, forcing removal")
				// Try to remove the worktree
				_ = runCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", currentWorktree)
			}
		}
	}
}

func cleanupWorkspace(ctx context.Context, repoRoot, workspaceDir, _ string) error {
	log.Info().
		Str("workspace_dir", workspaceDir).
		Msg("cleaning up git workspace (removing worktree only)")

	// Remove worktree only, keep the branch for restartable progress
	err := runCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", workspaceDir)
	if err != nil {
		log.Warn().Err(err).Str("workspace_dir", workspaceDir).Msg("failed to remove git worktree")
	}

	return nil
}
