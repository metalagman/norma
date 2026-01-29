package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

func MountWorktree(ctx context.Context, repoRoot, workspaceDir, branchName, baseBranch string) (string, error) {
	// Ensure we prune any stale worktrees before adding a new one.
	_ = RunCmdErr(ctx, repoRoot, "git", "worktree", "prune")

	// Check if we are in a git repo
	if !Available(ctx, repoRoot) {
		return "", fmt.Errorf("not a git repository: %s", repoRoot)
	}

	// Check if branch already exists
	branchExists := strings.TrimSpace(RunCmd(ctx, repoRoot, "git", "branch", "--list", branchName)) != ""

	if branchExists {
		// Ensure it's not checked out in another worktree
		ForceCleanupStaleWorktree(ctx, repoRoot, branchName)
	}

	args := []string{"worktree", "add", "-b", branchName, workspaceDir}
	if branchExists {
		args = []string{"worktree", "add", workspaceDir, branchName}
	} else if baseBranch != "" {
		args = append(args, baseBranch)
	}

	// Create worktree
	err := RunCmdErr(ctx, repoRoot, "git", args...)
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	if baseBranch != "" && branchName != baseBranch {
		if err := RunCmdErr(ctx, workspaceDir, "git", "merge", "--no-edit", baseBranch); err != nil {
			_ = RunCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", workspaceDir)
			return "", fmt.Errorf("git merge %s into %s: %w", baseBranch, branchName, err)
		}
	}

	return workspaceDir, nil
}

func ForceCleanupStaleWorktree(ctx context.Context, repoRoot, branchName string) {
	out := RunCmd(ctx, repoRoot, "git", "worktree", "list", "--porcelain")
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
				_ = RunCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", currentWorktree)
			}
		}
	}
}

func RemoveWorktree(ctx context.Context, repoRoot, workspaceDir string) error {
	// Remove worktree only, keep the branch for restartable progress
	err := RunCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", workspaceDir)
	if err != nil {
		log.Warn().Err(err).Str("workspace_dir", workspaceDir).Msg("failed to remove git worktree")
	}

	return err
}
