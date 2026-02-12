package pdca

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitWorkspaceChangesCommitsDirtyWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initTestRepo(t, ctx, repoRoot)

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\n")
	runGit(t, ctx, repoRoot, "add", "a.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\ntwo\n")
	writeTestFile(t, filepath.Join(repoRoot, "b.txt"), "new\n")

	if err := commitWorkspaceChanges(ctx, repoRoot, "run-1", "norma-8sl", 2); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))
	if after == before {
		t.Fatalf("expected a new commit, HEAD unchanged at %s", after)
	}

	commitMsg := runGit(t, ctx, repoRoot, "log", "-1", "--pretty=%B")
	if !strings.Contains(commitMsg, "chore: do step 002") {
		t.Fatalf("commit message missing step info:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Run: run-1") {
		t.Fatalf("commit message missing run id:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Task: norma-8sl") {
		t.Fatalf("commit message missing task id:\n%s", commitMsg)
	}

	status := strings.TrimSpace(runGit(t, ctx, repoRoot, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected clean workspace after commit, got:\n%s", status)
	}
}

func TestCommitWorkspaceChangesNoopForCleanWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initTestRepo(t, ctx, repoRoot)

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\n")
	runGit(t, ctx, repoRoot, "add", "a.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))

	if err := commitWorkspaceChanges(ctx, repoRoot, "run-2", "norma-8sl", 3); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("expected no commit for clean workspace; before=%s after=%s", before, after)
	}
}

func initTestRepo(t *testing.T, ctx context.Context, repoRoot string) {
	t.Helper()
	runGit(t, ctx, repoRoot, "init")
	runGit(t, ctx, repoRoot, "config", "user.name", "Norma Test")
	runGit(t, ctx, repoRoot, "config", "user.email", "norma-test@example.com")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func runGit(t *testing.T, ctx context.Context, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
