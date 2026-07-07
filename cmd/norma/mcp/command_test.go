package mcpcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/normahq/norma/v2/internal/task"
)

func TestCommandRegistersTasks(t *testing.T) {
	cmd := Command()
	sub, _, err := cmd.Find([]string{"tasks"})
	if err != nil {
		t.Fatalf("Find(tasks) error = %v", err)
	}
	if sub == nil || sub.Name() != "tasks" {
		t.Fatalf("tasks subcommand = %v, want tasks", sub)
	}
}

func TestTasksCommandUsesWorkingDirFlag(t *testing.T) {
	var gotWorkingDir string
	prevNewTracker := newTracker
	prevRunTasksServer := runTasksServer
	t.Cleanup(func() {
		newTracker = prevNewTracker
		runTasksServer = prevRunTasksServer
	})

	newTracker = func(workingDir string) task.Tracker {
		gotWorkingDir = workingDir
		return &task.BeadsTracker{}
	}
	runTasksServer = func(_ context.Context, _ task.Tracker) error {
		return nil
	}

	cmd := TasksCommand()
	workingDir := filepath.Join(t.TempDir(), "repo")
	cmd.SetArgs([]string{"--working-dir", workingDir})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	want := filepath.Clean(workingDir)
	got := filepath.Clean(gotWorkingDir)
	if got != want {
		t.Fatalf("working directory = %q, want %q", got, want)
	}
}

func TestTasksCommandDefaultsWorkingDirToCurrentDirectory(t *testing.T) {
	var gotWorkingDir string
	prevNewTracker := newTracker
	prevRunTasksServer := runTasksServer
	t.Cleanup(func() {
		newTracker = prevNewTracker
		runTasksServer = prevRunTasksServer
	})

	newTracker = func(workingDir string) task.Tracker {
		gotWorkingDir = workingDir
		return &task.BeadsTracker{}
	}
	runTasksServer = func(_ context.Context, _ task.Tracker) error {
		return nil
	}

	workingDir := t.TempDir()
	prevWD, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("filepath.Abs(.) error = %v", err)
	}
	if err := chdirForTest(workingDir); err != nil {
		t.Fatalf("chdir working dir: %v", err)
	}
	t.Cleanup(func() {
		_ = chdirForTest(prevWD)
	})

	cmd := TasksCommand()
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	want := filepath.Clean(workingDir)
	got := filepath.Clean(gotWorkingDir)
	if got != want {
		t.Fatalf("working directory = %q, want %q", got, want)
	}
}

func chdirForTest(path string) error {
	return os.Chdir(path)
}
