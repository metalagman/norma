package relay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
