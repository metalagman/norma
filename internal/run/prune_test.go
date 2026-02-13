package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internaldb "github.com/metalagman/norma/internal/db"
)

func TestPruneRemovesOnlyStaleNormaTaskBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initGitRepo(t, ctx, repoRoot)

	writeFile(t, filepath.Join(repoRoot, "base.txt"), "base\n")
	runGit(t, ctx, repoRoot, "add", "base.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: seed repo")

	// Stale branch: exists locally, checked out nowhere.
	runGit(t, ctx, repoRoot, "branch", "norma/task/norma-stale")

	// Active branch: attached to a live worktree, so it must be preserved.
	activeWorktree := filepath.Join(t.TempDir(), "active-worktree")
	runGit(t, ctx, repoRoot, "worktree", "add", "-b", "norma/task/norma-active", activeWorktree)

	// Norma run branch/worktree: should be removed by prune.
	runWorkspace := filepath.Join(repoRoot, ".norma", "runs", "run-1", "steps", "001-do", "workspace")
	if err := os.MkdirAll(filepath.Dir(runWorkspace), 0o755); err != nil {
		t.Fatalf("mkdir run workspace parent: %v", err)
	}
	runGit(t, ctx, repoRoot, "worktree", "add", "-b", "norma/task/norma-run", runWorkspace)

	dbPath := filepath.Join(t.TempDir(), "norma.db")
	database, err := internaldb.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := Prune(ctx, database, repoRoot); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if got := strings.TrimSpace(runGit(t, ctx, repoRoot, "branch", "--list", "norma/task/norma-stale")); got != "" {
		t.Fatalf("stale branch should be deleted, got %q", got)
	}
	if got := strings.TrimSpace(runGit(t, ctx, repoRoot, "branch", "--list", "norma/task/norma-run")); got != "" {
		t.Fatalf("run branch should be deleted, got %q", got)
	}
	if got := strings.TrimSpace(runGit(t, ctx, repoRoot, "branch", "--list", "norma/task/norma-active")); got == "" {
		t.Fatalf("active branch should be preserved")
	}

	if _, err := os.Stat(runWorkspace); !os.IsNotExist(err) {
		t.Fatalf("expected run workspace to be removed, stat err=%v", err)
	}
}

