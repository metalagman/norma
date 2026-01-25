package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

func createWorkspace(ctx context.Context, repoRoot, runDir, issueID string) (string, error) {
	workspaceDir := filepath.Join(runDir, "workspace")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}

	// We create a temporary branch for the worktree to avoid collisions.
	// We include the task/issue ID and the run ID in the branch name.
	runID := filepath.Base(runDir)
	branchName := fmt.Sprintf("norma/%s/%s", issueID, runID)
	
	log.Info().
		Str("repo_root", repoRoot).
		Str("workspace_dir", workspaceDir).
		Str("branch", branchName).
		Msg("creating git workspace")

	// Check if we are in a git repo
	if !gitAvailable(ctx, repoRoot) {
		return "", fmt.Errorf("not a git repository: %s", repoRoot)
	}

	// Create worktree: git worktree add -b <branch> <path>
	err := runCmdErr(ctx, repoRoot, "git", "worktree", "add", "-b", branchName, workspaceDir)
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return workspaceDir, nil
}

func cleanupWorkspace(ctx context.Context, repoRoot, workspaceDir, issueID string) error {
	runID := filepath.Base(filepath.Dir(workspaceDir))
	branchName := fmt.Sprintf("norma/%s/%s", issueID, runID)
	
	log.Info().
		Str("workspace_dir", workspaceDir).
		Str("branch", branchName).
		Msg("cleaning up git workspace")

	// Remove worktree
	err := runCmdErr(ctx, repoRoot, "git", "worktree", "remove", "--force", workspaceDir)
	if err != nil {
		log.Warn().Err(err).Str("workspace_dir", workspaceDir).Msg("failed to remove git worktree")
	}
	
	// Delete temporary branch
	err = runCmdErr(ctx, repoRoot, "git", "branch", "-D", branchName)
	if err != nil {
		log.Warn().Err(err).Str("branch", branchName).Msg("failed to delete git branch")
	}
	
	return nil
}

func getWorkspacePatch(ctx context.Context, workspaceDir string) (string, error) {
	log.Debug().Str("workspace_dir", workspaceDir).Msg("extracting patch from workspace")

	// Generate diff between current state and HEAD
	diff := runCmd(ctx, workspaceDir, "git", "diff", "HEAD")
	
	// Also check for untracked files
	untracked := runCmd(ctx, workspaceDir, "git", "ls-files", "--others", "--exclude-standard")
	if strings.TrimSpace(untracked) != "" {
		log.Debug().Str("workspace_dir", workspaceDir).Msg("including untracked files in patch")
		// If there are untracked files, we need to add them to the index to include them in the diff
		err := runCmdErr(ctx, workspaceDir, "git", "add", "-N", ".")
		if err != nil {
			return "", fmt.Errorf("git add -N: %w", err)
		}
		diff = runCmd(ctx, workspaceDir, "git", "diff", "HEAD")
	}
	
	return diff, nil
}
