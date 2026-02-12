package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyChangesDoesNotCommitRestoredLocalChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initGitRepo(t, ctx, repoRoot)

	writeFile(t, filepath.Join(repoRoot, "base.txt"), "base\n")
	writeFile(t, filepath.Join(repoRoot, "local.txt"), "clean\n")
	runGit(t, ctx, repoRoot, "add", "-A")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: initial")

	branchName := "norma/task/norma-wzw"
	runGit(t, ctx, repoRoot, "checkout", "-b", branchName)
	writeFile(t, filepath.Join(repoRoot, "base.txt"), "base\nbranch\n")
	runGit(t, ctx, repoRoot, "add", "base.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "feat: branch change")
	runGit(t, ctx, repoRoot, "checkout", "master")

	// Simulate local uncommitted work that must survive applyChanges.
	writeFile(t, filepath.Join(repoRoot, "local.txt"), "dirty-local\n")
	writeFile(t, filepath.Join(repoRoot, "scratch.txt"), "scratch\n")

	runner := &Runner{repoRoot: repoRoot}
	if err := runner.applyChanges(ctx, "run-1", "merge branch", "norma-wzw"); err != nil {
		t.Fatalf("applyChanges() error = %v", err)
	}

	committedFiles := runGit(t, ctx, repoRoot, "show", "--name-only", "--pretty=format:", "HEAD")
	if strings.Contains(committedFiles, "local.txt") {
		t.Fatalf("local dirty file unexpectedly included in commit:\n%s", committedFiles)
	}
	if strings.Contains(committedFiles, "scratch.txt") {
		t.Fatalf("local untracked file unexpectedly included in commit:\n%s", committedFiles)
	}
	if !strings.Contains(committedFiles, "base.txt") {
		t.Fatalf("expected merged file base.txt in commit:\n%s", committedFiles)
	}

	localContent := readFile(t, filepath.Join(repoRoot, "local.txt"))
	if localContent != "dirty-local\n" {
		t.Fatalf("local.txt content mismatch, got %q", localContent)
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "scratch.txt")); err != nil {
		t.Fatalf("expected scratch.txt to be restored: %v", err)
	}

	status := runGit(t, ctx, repoRoot, "status", "--porcelain")
	if !strings.Contains(status, " M local.txt") {
		t.Fatalf("expected local.txt to remain dirty after applyChanges; status:\n%s", status)
	}
	if !strings.Contains(status, "?? scratch.txt") {
		t.Fatalf("expected scratch.txt to remain untracked after applyChanges; status:\n%s", status)
	}

	stashList := strings.TrimSpace(runGit(t, ctx, repoRoot, "stash", "list"))
	if stashList != "" {
		t.Fatalf("expected no leftover stash entries, got:\n%s", stashList)
	}
}

func initGitRepo(t *testing.T, ctx context.Context, repoRoot string) {
	t.Helper()
	runGit(t, ctx, repoRoot, "init")
	runGit(t, ctx, repoRoot, "config", "user.name", "Norma Test")
	runGit(t, ctx, repoRoot, "config", "user.email", "norma-test@example.com")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(data)
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
