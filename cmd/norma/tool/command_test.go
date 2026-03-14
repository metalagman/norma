package toolcmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
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

	mcpDumpSub, _, err := cmd.Find([]string{"mcp-dump"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if mcpDumpSub == nil || mcpDumpSub.Name() != "mcp-dump" {
		t.Fatalf("subcommand = %v, want mcp-dump", mcpDumpSub)
	}
	if got := mcpDumpSub.Flags().Lookup("json"); got == nil {
		t.Fatalf("expected --json flag on mcp-dump command")
	}
	if got := mcpDumpSub.Flags().Lookup("model"); got != nil {
		t.Fatalf("did not expect --model flag on mcp-dump command")
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
	if got := acpReplSub.Flags().Lookup("model"); got == nil {
		t.Fatalf("expected --model flag on acp-repl command")
	}
	if got := acpReplSub.Flags().Lookup("mode"); got == nil {
		t.Fatalf("expected --mode flag on acp-repl command")
	}

	sub, _, err := cmd.Find([]string{"codex-acp-bridge"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != "codex-acp-bridge" {
		t.Fatalf("subcommand = %v, want codex-acp-bridge", sub)
	}
	if got := sub.Flags().Lookup("name"); got == nil {
		t.Fatalf("expected --name flag on codex-acp-bridge command")
	}
	if got := sub.Flags().Lookup("codex-model"); got == nil {
		t.Fatalf("expected --codex-model flag on codex-acp-bridge command")
	}
	if got := sub.Flags().Lookup("codex-arg"); got != nil {
		t.Fatalf("did not expect deprecated --codex-arg flag on codex-acp-bridge command")
	}
	if err := sub.Args(sub, []string{"--", "--trace"}); err == nil {
		t.Fatalf("expected codex-acp-bridge to reject positional arguments")
	}
}

func TestACPDumpCommand_RejectsMissingDelimiter(t *testing.T) {
	var calls int
	prev := runACPDumpInspector
	t.Cleanup(func() { runACPDumpInspector = prev })
	runACPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
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
	runACPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
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
	runACPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
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
	testDumpCommandPassThrough(
		t,
		acpDumpToolCommand,
		[]string{"--json", "--", "opencode", "acp", "--trace"},
		[]string{"opencode", "acp", "--trace"},
		func(inspector dumpInspectorFunc) func() {
			prev := runACPDumpInspector
			runACPDumpInspector = inspector
			return func() { runACPDumpInspector = prev }
		},
	)
}

func TestMCPDumpCommand_RejectsMissingDelimiter(t *testing.T) {
	var calls int
	prev := runMCPDumpInspector
	t.Cleanup(func() { runMCPDumpInspector = prev })
	runMCPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := mcpDumpToolCommand()
	cmd.SetArgs([]string{"codex", "mcp-server"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing command delimiter --") {
		t.Fatalf("error = %v, want missing delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestMCPDumpCommand_RejectsArgsBeforeDelimiter(t *testing.T) {
	var calls int
	prev := runMCPDumpInspector
	t.Cleanup(func() { runMCPDumpInspector = prev })
	runMCPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := mcpDumpToolCommand()
	cmd.SetArgs([]string{"oops", "--", "codex", "mcp-server"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "arguments before -- are not allowed") {
		t.Fatalf("error = %v, want args-before-delimiter error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestMCPDumpCommand_RejectsMissingCommandAfterDelimiter(t *testing.T) {
	var calls int
	prev := runMCPDumpInspector
	t.Cleanup(func() { runMCPDumpInspector = prev })
	runMCPDumpInspector = func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error {
		calls++
		return nil
	}

	cmd := mcpDumpToolCommand()
	cmd.SetArgs([]string{"--"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "mcp server command is required after --") {
		t.Fatalf("error = %v, want missing command error", err)
	}
	if calls != 0 {
		t.Fatalf("run inspector called %d times, want 0", calls)
	}
}

func TestMCPDumpCommand_PassesThroughArgsAfterDelimiter(t *testing.T) {
	testDumpCommandPassThrough(
		t,
		mcpDumpToolCommand,
		[]string{"--json", "--", "codex", "mcp-server", "--trace"},
		[]string{"codex", "mcp-server", "--trace"},
		func(inspector dumpInspectorFunc) func() {
			prev := runMCPDumpInspector
			runMCPDumpInspector = inspector
			return func() { runMCPDumpInspector = prev }
		},
	)
}

type dumpInspectorFunc func(context.Context, string, []string, bool, zerolog.Level, io.Writer, io.Writer) error

func testDumpCommandPassThrough(
	t *testing.T,
	newCommand func() *cobra.Command,
	args []string,
	wantCommand []string,
	setInspector func(dumpInspectorFunc) func(),
) {
	t.Helper()

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
		workingDir string
		command    []string
		jsonOutput bool
		calls      int
	}
	restoreInspector := setInspector(func(_ context.Context, workingDir string, command []string, jsonOutput bool, _ zerolog.Level, _ io.Writer, _ io.Writer) error {
		got.workingDir = workingDir
		got.command = append([]string(nil), command...)
		got.jsonOutput = jsonOutput
		got.calls++
		return nil
	})
	t.Cleanup(restoreInspector)

	cmd := newCommand()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.calls != 1 {
		t.Fatalf("run inspector called %d times, want 1", got.calls)
	}
	if got.workingDir != tempDir {
		t.Fatalf("working dir = %q, want %q", got.workingDir, tempDir)
	}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
	if !got.jsonOutput {
		t.Fatalf("jsonOutput = false, want true")
	}
	if filepath.Base(got.workingDir) == "" {
		t.Fatalf("working dir should be non-empty")
	}
}

func TestACPREPLCommand_RejectsMissingDelimiter(t *testing.T) {
	var calls int
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(context.Context, string, []string, string, string, zerolog.Level, io.Reader, io.Writer, io.Writer) error {
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
	runACPREPL = func(context.Context, string, []string, string, string, zerolog.Level, io.Reader, io.Writer, io.Writer) error {
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
	runACPREPL = func(context.Context, string, []string, string, string, zerolog.Level, io.Reader, io.Writer, io.Writer) error {
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
		workingDir string
		command    []string
		model      string
		mode       string
		calls      int
	}
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(_ context.Context, workingDir string, command []string, sessionModel, sessionMode string, _ zerolog.Level, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got.workingDir = workingDir
		got.command = append([]string(nil), command...)
		got.model = sessionModel
		got.mode = sessionMode
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
	if got.workingDir != tempDir {
		t.Fatalf("working dir = %q, want %q", got.workingDir, tempDir)
	}
	wantCommand := []string{"opencode", "acp", "--trace"}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
	if got.model != "" {
		t.Fatalf("model = %q, want empty", got.model)
	}
	if got.mode != "" {
		t.Fatalf("mode = %q, want empty", got.mode)
	}
}

func TestACPREPLCommand_PassesThroughModelFlag(t *testing.T) {
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
		command []string
		model   string
		mode    string
		calls   int
	}
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(_ context.Context, _ string, command []string, sessionModel, sessionMode string, _ zerolog.Level, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got.command = append([]string(nil), command...)
		got.model = sessionModel
		got.mode = sessionMode
		got.calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"--model", "openai/gpt-5.4", "--", "opencode", "acp"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.calls != 1 {
		t.Fatalf("run repl called %d times, want 1", got.calls)
	}
	if got.model != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want openai/gpt-5.4", got.model)
	}
	wantCommand := []string{"opencode", "acp"}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
	if got.mode != "" {
		t.Fatalf("mode = %q, want empty", got.mode)
	}
}

func TestACPREPLCommand_PassesThroughModeFlag(t *testing.T) {
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
		command []string
		model   string
		mode    string
		calls   int
	}
	prev := runACPREPL
	t.Cleanup(func() { runACPREPL = prev })
	runACPREPL = func(_ context.Context, _ string, command []string, sessionModel, sessionMode string, _ zerolog.Level, _ io.Reader, _ io.Writer, _ io.Writer) error {
		got.command = append([]string(nil), command...)
		got.model = sessionModel
		got.mode = sessionMode
		got.calls++
		return nil
	}

	cmd := acpReplToolCommand()
	cmd.SetArgs([]string{"--model", "openai/gpt-5.4", "--mode", "code", "--", "opencode", "acp"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.calls != 1 {
		t.Fatalf("run repl called %d times, want 1", got.calls)
	}
	if got.model != "openai/gpt-5.4" {
		t.Fatalf("model = %q, want openai/gpt-5.4", got.model)
	}
	if got.mode != "code" {
		t.Fatalf("mode = %q, want code", got.mode)
	}
	wantCommand := []string{"opencode", "acp"}
	if strings.Join(got.command, "|") != strings.Join(wantCommand, "|") {
		t.Fatalf("command = %v, want %v", got.command, wantCommand)
	}
}
