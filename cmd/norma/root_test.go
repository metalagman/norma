package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInitBeads_SkipsWhenBeadsExistsAtGitTopLevel(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")

	if err := os.MkdirAll(filepath.Join(repoRoot, ".beads"), 0o700); err != nil {
		t.Fatalf("create .beads: %v", err)
	}

	nestedDir := filepath.Join(repoRoot, "nested", "dir")
	if err := os.MkdirAll(nestedDir, 0o700); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	t.Cleanup(func() {
		if chErr := os.Chdir(prevWD); chErr != nil {
			t.Fatalf("restore wd: %v", chErr)
		}
	})
	if err := os.Chdir(nestedDir); err != nil {
		t.Fatalf("chdir nested: %v", err)
	}

	called := 0
	prevRunner := runBeadsInit
	t.Cleanup(func() { runBeadsInit = prevRunner })
	runBeadsInit = func(_ context.Context, _ string) error {
		called++
		return nil
	}

	if err := initBeads(context.Background()); err != nil {
		t.Fatalf("initBeads returned error: %v", err)
	}
	if called != 0 {
		t.Fatalf("runBeadsInit called %d times, want 0", called)
	}
}

func TestInitBeads_InitializesWhenMissing(t *testing.T) {
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	t.Cleanup(func() {
		if chErr := os.Chdir(prevWD); chErr != nil {
			t.Fatalf("restore wd: %v", chErr)
		}
	})
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}

	called := 0
	gotRepoRoot := ""
	prevRunner := runBeadsInit
	t.Cleanup(func() { runBeadsInit = prevRunner })
	runBeadsInit = func(_ context.Context, root string) error {
		called++
		gotRepoRoot = root
		return nil
	}

	if err := initBeads(context.Background()); err != nil {
		t.Fatalf("initBeads returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("runBeadsInit called %d times, want 1", called)
	}
	if gotRepoRoot != repoRoot {
		t.Fatalf("runBeadsInit repoRoot = %q, want %q", gotRepoRoot, repoRoot)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
