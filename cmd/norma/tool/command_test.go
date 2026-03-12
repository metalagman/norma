package toolcmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandRegistered(t *testing.T) {
	cmd := Command()

	acpDumpSub, _, err := cmd.Find([]string{"acp-dump"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if acpDumpSub == nil || acpDumpSub.Name() != "acp-dump" {
		t.Fatalf("subcommand = %v, want acp-dump", acpDumpSub)
	}
	if got := acpDumpSub.Flags().Lookup("json"); got == nil {
		t.Fatalf("expected --json flag on acp-dump command")
	}
	if got := acpDumpSub.Flags().Lookup("model"); got != nil {
		t.Fatalf("did not expect --model flag on acp-dump command")
	}
	if sub, _, err := cmd.Find([]string{"acpdump"}); err == nil && sub != nil {
		t.Fatalf("did not expect legacy acpdump command to be registered")
	}

	acpReplSub, _, err := cmd.Find([]string{"acp-repl"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if acpReplSub == nil || acpReplSub.Name() != "acp-repl" {
		t.Fatalf("subcommand = %v, want acp-repl", acpReplSub)
	}
	if got := acpReplSub.Flags().Lookup("json"); got != nil {
		t.Fatalf("did not expect --json flag on acp-repl command")
	}

	sub, _, err := cmd.Find([]string{"codex-acp"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != "codex-acp" {
		t.Fatalf("subcommand = %v, want codex-acp", sub)
	}
	if got := sub.Flags().Lookup("name"); got == nil {
		t.Fatalf("expected --name flag on codex-acp command")
	}
	if got := sub.Flags().Lookup("codex-model"); got == nil {
		t.Fatalf("expected --codex-model flag on codex-acp command")
	}
	if got := sub.Flags().Lookup("codex-arg"); got != nil {
		t.Fatalf("did not expect deprecated --codex-arg flag on codex-acp command")
	}
	if err := sub.Args(sub, []string{"--", "--trace"}); err == nil {
		t.Fatalf("expected codex-acp to reject positional arguments")
	}
}

func TestACPDumpCommand_RejectsMissingDelimiter(t *testing.T) {
	var calls int
	prev := runACPDumpInspector
	t.Cleanup(func() { runACPDumpInspector = prev })
	runACPDumpInspector = func(context.Context, string, []string, bool, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpDumpToolCommand()
	cmd.SetArgs([]string{"opencode", "acp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing command delimiter --") {
		t.Fatalf("error = %v, want missing delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestACPDumpCommand_RejectsArgsBeforeDelimiter(t *testing.T) {
	var calls int
	prev := runACPDumpInspector
	t.Cleanup(func() { runACPDumpInspector = prev })
	runACPDumpInspector = func(context.Context, string, []string, bool, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpDumpToolCommand()
	cmd.SetArgs([]string{"oops", "--", "opencode", "acp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "arguments before -- are not allowed") {
		t.Fatalf("error = %v, want args-before-delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestACPDumpCommand_RejectsMissingCommandAfterDelimiter(t *testing.T) {
	var calls int
	prev := runACPDumpInspector
	t.Cleanup(func() { runACPDumpInspector = prev })
	runACPDumpInspector = func(context.Context, string, []string, bool, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpDumpToolCommand()
	cmd.SetArgs([]string{"--"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "acp server command is required after --") {
		t.Fatalf("error = %v, want missing command error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestACPDumpCommand_PassesThroughArgsAfterDelimiter(t *testing.T) {
	tempDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(prevWD); chdirErr != nil {
			t.Fatalf("restore wd: %v", chdirErr)
		}
	})
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}

	var got struct {
		repoRoot   string
		command    []string
		jsonOutput bool
		calls      int
	}
	prev := runACPDumpInspector
	t.Cleanup(func() { runACPDumpInspector = prev })
	runACPDumpInspector = func(_ context.Context, repoRoot string, command []string, jsonOutput bool, _ io.Writer, _ io.Writer) error {
		got.repoRoot = repoRoot
		got.command = append([]string(nil), command...)
		got.jsonOutput = jsonOutput
		got.calls++
		return nil
	}

	cmd := acpDumpToolCommand()
	cmd.SetArgs([]string{"--json", "--", "opencode", "acp", "--trace"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.calls != 1 {
		t.Fatalf("run inspector called %d times, want 1", got.calls)
	}
	if got.repoRoot != tempDir {
		t.Fatalf("repo root = %q, want %q", got.repoRoot, tempDir)
	}
	wantCommand := []string{"opencode", "acp", "--trace"}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
	if !got.jsonOutput {
		t.Fatalf("jsonOutput = false, want true")
	}
	if filepath.Base(got.repoRoot) == "" {
		t.Fatalf("repo root should be non-empty")
	}
}

func TestACPREPLCommand_RejectsMissingDelimiter(t *testing.T) {
	var calls int
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"opencode", "acp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing command delimiter --") {
		t.Fatalf("error = %v, want missing delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run repl called %d times, want 0", calls)
	}
}

func TestACPREPLCommand_RejectsArgsBeforeDelimiter(t *testing.T) {
	var calls int
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"oops", "--", "opencode", "acp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "arguments before -- are not allowed") {
		t.Fatalf("error = %v, want args-before-delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run repl called %d times, want 0", calls)
	}
}

func TestACPREPLCommand_RejectsMissingCommandAfterDelimiter(t *testing.T) {
	var calls int
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"--"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "acp server command is required after --") {
		t.Fatalf("error = %v, want missing command error", err)
	}
	if calls != 0 {
		t.Fatalf("run repl called %d times, want 0", calls)
	}
}

func TestACPREPLCommand_PassesThroughArgsAfterDelimiter(t *testing.T) {
	tempDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(prevWD); chdirErr != nil {
			t.Fatalf("restore wd: %v", chdirErr)
		}
	})
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}

	var got struct {
		repoRoot string
		command  []string
		calls    int
	}
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(_ context.Context, repoRoot string, command []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got.repoRoot = repoRoot
		got.command = append([]string(nil), command...)
		got.calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"--", "opencode", "acp", "--trace"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.calls != 1 {
		t.Fatalf("run repl called %d times, want 1", got.calls)
	}
	if got.repoRoot != tempDir {
		t.Fatalf("repo root = %q, want %q", got.repoRoot, tempDir)
	}
	wantCommand := []string{"opencode", "acp", "--trace"}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
}
