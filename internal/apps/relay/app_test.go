package relay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	testWorkspaceBaseBranchSourceConfig = "config"
	testWorkspaceBaseBranchSourceHead   = "head"
)

func TestResolveWorkingDir_EmptyUsesProcessCWD(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	got, err := resolveWorkingDir("")
	if err != nil {
		t.Fatalf("resolveWorkingDir returned error: %v", err)
	}
	if got != filepath.Clean(cwd) {
		t.Fatalf("resolveWorkingDir(\"\") = %q, want %q", got, filepath.Clean(cwd))
	}
}

func TestResolveWorkingDir_RelativeBecomesAbsolute(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	got, err := resolveWorkingDir(".")
	if err != nil {
		t.Fatalf("resolveWorkingDir returned error: %v", err)
	}
	if got != filepath.Clean(cwd) {
		t.Fatalf("resolveWorkingDir(\".\") = %q, want %q", got, filepath.Clean(cwd))
	}
}

func TestResolveStateDir_RelativeUsesWorkingDir(t *testing.T) {
	workingDir := "/tmp/norma-relay-work"

	got, err := resolveStateDir(workingDir, ".config/relay")
	if err != nil {
		t.Fatalf("resolveStateDir returned error: %v", err)
	}

	want, err := filepath.Abs(filepath.Join(workingDir, ".config/relay"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolveStateDir() = %q, want %q", got, filepath.Clean(want))
	}
}

func TestResolveStateDir_RequiresValue(t *testing.T) {
	if _, err := resolveStateDir("/tmp/norma-relay-work", ""); err == nil {
		t.Fatal("resolveStateDir returned nil error for empty state_dir")
	}
}

func TestIsExpectedBotRunShutdown(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: true,
		},
		{
			name: "wrapped context canceled",
			err:  fmt.Errorf("shutdown: %w", context.Canceled),
			want: true,
		},
		{
			name: "other error",
			err:  context.DeadlineExceeded,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpectedBotRunShutdown(tt.err); got != tt.want {
				t.Fatalf("isExpectedBotRunShutdown(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestNormalizeAgentSystemInstructions(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]string
		want map[string]string
	}{
		{
			name: "nil",
			raw:  nil,
			want: nil,
		},
		{
			name: "empty",
			raw:  map[string]string{},
			want: nil,
		},
		{
			name: "trim_and_filter",
			raw: map[string]string{
				" alpha ": "  do this  ",
				"beta":    " \n\t ",
				"  ":      "value",
				"gamma":   "ok",
			},
			want: map[string]string{
				"alpha": "do this",
				"gamma": "ok",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAgentSystemInstructions(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeAgentSystemInstructions(%#v) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveWorkspaceBaseBranch_ConfigPreferredWhenValid(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()
	initGitRepoForRelay(t, ctx, repoDir)

	runGitForRelay(t, ctx, repoDir, "branch", "main")

	branch, source, err := resolveWorkspaceBaseBranch(ctx, repoDir, "main", true)
	if err != nil {
		t.Fatalf("resolveWorkspaceBaseBranch returned error: %v", err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}
	if source != testWorkspaceBaseBranchSourceConfig {
		t.Fatalf("source = %q, want %s", source, testWorkspaceBaseBranchSourceConfig)
	}
}

func TestResolveWorkspaceBaseBranch_FallbackToHeadWhenConfiguredMissing(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()
	initGitRepoForRelay(t, ctx, repoDir)
	runGitForRelay(t, ctx, repoDir, "checkout", "-b", "trunk")

	branch, source, err := resolveWorkspaceBaseBranch(ctx, repoDir, "missing-branch", true)
	if err != nil {
		t.Fatalf("resolveWorkspaceBaseBranch returned error: %v", err)
	}
	if branch != "trunk" {
		t.Fatalf("branch = %q, want trunk", branch)
	}
	if source != testWorkspaceBaseBranchSourceHead {
		t.Fatalf("source = %q, want %s", source, testWorkspaceBaseBranchSourceHead)
	}
}

func TestResolveWorkspaceBaseBranch_EnabledRequiresResolvableBranch(t *testing.T) {
	ctx := context.Background()
	workingDir := t.TempDir()

	if _, _, err := resolveWorkspaceBaseBranch(ctx, workingDir, "", true); err == nil {
		t.Fatal("resolveWorkspaceBaseBranch returned nil error for non-git workspace-enabled config")
	}
}

func initGitRepoForRelay(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	runGitForRelay(t, ctx, dir, "init")
	runGitForRelay(t, ctx, dir, "config", "user.name", "Norma Test")
	runGitForRelay(t, ctx, dir, "config", "user.email", "norma-test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGitForRelay(t, ctx, dir, "add", "seed.txt")
	runGitForRelay(t, ctx, dir, "commit", "-m", "chore: seed")
}

func runGitForRelay(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
