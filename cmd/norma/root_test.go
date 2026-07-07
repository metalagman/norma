package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommandUsesNormaMetadata(t *testing.T) {
	if rootCmd.Use != "norma" {
		t.Fatalf("root use = %q, want norma", rootCmd.Use)
	}
	if !strings.Contains(rootCmd.Short, "norma") {
		t.Fatalf("root short = %q, want norma description", rootCmd.Short)
	}
	if strings.Contains(rootCmd.Short, "codex") {
		t.Fatalf("root short = %q, want no codex metadata", rootCmd.Short)
	}
	if rootCmd.Version == "" {
		t.Fatal("root version is empty")
	}
}

func TestVersionStringUsesExplicitVersion(t *testing.T) {
	prev := version
	version = "v9.9.9"
	t.Cleanup(func() { version = prev })

	if got := versionString(); got != "v9.9.9" {
		t.Fatalf("versionString() = %q, want v9.9.9", got)
	}
}

func TestInitBeads_SkipsWhenBeadsExistsAtGitTopLevel(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")

	if err := os.MkdirAll(filepath.Join(workingDir, ".beads"), 0o700); err != nil {
		t.Fatalf("create .beads: %v", err)
	}

	nestedDir := filepath.Join(workingDir, "nested", "dir")
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
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	t.Cleanup(func() {
		if chErr := os.Chdir(prevWD); chErr != nil {
			t.Fatalf("restore wd: %v", chErr)
		}
	})
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("chdir working directory: %v", err)
	}

	called := 0
	gotWorkingDir := ""
	prevRunner := runBeadsInit
	t.Cleanup(func() { runBeadsInit = prevRunner })
	runBeadsInit = func(_ context.Context, root string) error {
		called++
		gotWorkingDir = root
		return nil
	}

	if err := initBeads(context.Background()); err != nil {
		t.Fatalf("initBeads returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("runBeadsInit called %d times, want 1", called)
	}
	if gotWorkingDir != workingDir {
		t.Fatalf("runBeadsInit workingDir = %q, want %q", gotWorkingDir, workingDir)
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
