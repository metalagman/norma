// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

func mountWorktree(ctx context.Context, repoRoot, workspaceDir, branchName string) (string, error) {
	// Ensure we prune any stale worktrees before adding a new one.
	_ = runCmdErr(ctx, repoRoot, "git", "worktree", "prune")

	// Check if we are in a git repo
	if !gitAvailable(ctx, repoRoot) {
		return "", fmt.Errorf("not a git repository: %s", repoRoot)
	}

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

func removeWorktree(ctx context.Context, repoRoot, workspaceDir string) error {
	// Remove worktree only, keep the branch for restartable progress
	err := runCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", workspaceDir)
	if err != nil {
		log.Warn().Err(err).Str("workspace_dir", workspaceDir).Msg("failed to remove git worktree")
	}

	return err
}
