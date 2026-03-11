package playgroundcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpcmd "github.com/metalagman/norma/cmd/norma/playground/acp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	methodInitialize        = "initialize"
	methodSessionNew        = "session/new"
	methodSessionSetModel   = "session/set_model"
	methodSessionPrompt     = "session/prompt"
	methodSessionCancel     = "session/cancel"
	methodSessionUpdate     = "session/update"
	updateAgentMessageChunk = "agent_message_chunk"
	sessionOneHelloResponse = "session-1:hello\n"
	acpSubcommandPECL       = "pecl"
	acpSubcommandGemini     = "gemini"
	acpSubcommandOpenCode   = "opencode"
	acpSubcommandCodex      = "codex"
	acpSubcommandInfo       = "info"
	acpSubcommandWeb        = "web"
	structuredSubcmd        = "structured"
)

func TestPlaygroundCommandRegistered(t *testing.T) {
	cmd := Command()
	sub, _, err := cmd.Find([]string{"acp"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != "acp" {
		t.Fatalf("subcommand = %v, want acp", sub)
	}

	sub, _, err = cmd.Find([]string{"acp", acpSubcommandPECL})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandPECL {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandPECL)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandPECL, acpSubcommandGemini})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandGemini {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandGemini)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandPECL, acpSubcommandOpenCode})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandOpenCode {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandOpenCode)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandPECL, acpSubcommandCodex})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandCodex {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandCodex)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandInfo})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandInfo {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandInfo)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandInfo, acpSubcommandGemini})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandGemini {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandGemini)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandInfo, acpSubcommandOpenCode})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandOpenCode {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandOpenCode)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandInfo, acpSubcommandCodex})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandCodex {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandCodex)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandWeb})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandWeb {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandWeb)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandWeb, acpSubcommandGemini})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandGemini {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandGemini)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandWeb, acpSubcommandOpenCode})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandOpenCode {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandOpenCode)
	}
	sub, _, err = cmd.Find([]string{"acp", acpSubcommandWeb, acpSubcommandCodex})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != acpSubcommandCodex {
		t.Fatalf("subcommand = %v, want %s", sub, acpSubcommandCodex)
	}
	sub, _, err = cmd.Find([]string{"codex-mcp-server"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != "codex-mcp-server" {
		t.Fatalf("subcommand = %v, want codex-mcp-server", sub)
	}
	sub, _, err = cmd.Find([]string{structuredSubcmd})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != structuredSubcmd {
		t.Fatalf("subcommand = %v, want %s", sub, structuredSubcmd)
	}
}

func TestPlaygroundGeminiACPDoesNotExposeLegacyDebugFlags(t *testing.T) {
	cmd := acpcmd.GeminiCommand()
	if got := cmd.Flags().Lookup("verbose"); got != nil {
		t.Fatalf("verbose flag should be removed, got %v", got.Name)
	}
	if got := cmd.Flags().Lookup("debug-events"); got != nil {
		t.Fatalf("debug-events flag should be removed, got %v", got.Name)
	}
}

func TestRunGeminiACPOneShot(t *testing.T) {
	wrapper, argsFile := writeGeminiWrapper(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := acpcmd.RunGeminiACP(context.Background(), t.TempDir(), acpcmd.GeminiOptions{
		Prompt:     "hello",
		Model:      "gemini-test",
		GeminiBin:  wrapper,
		GeminiArgs: []string{"--sandbox", "workspace-write"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runGeminiACP() error = %v", err)
	}

	if got := stdout.String(); got != sessionOneHelloResponse {
		t.Fatalf("stdout = %q, want %q", got, sessionOneHelloResponse)
	}
	if got := stderr.String(); !strings.Contains(got, "starting Gemini ACP playground") {
		t.Fatalf("stderr = %q, want lifecycle log entry", got)
	}
	args := readArgsFile(t, argsFile)
	wantArgs := []string{"--experimental-acp", "--model", "gemini-test", "--sandbox", "workspace-write"}
	for _, want := range wantArgs {
		if !containsArg(args, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
}

func TestRunGeminiACPReusesSessionInREPL(t *testing.T) {
	wrapper, _ := writeGeminiWrapper(t)
	testACPSessionReuseInREPL(t, func(ctx context.Context, repoRoot string, input io.Reader, stdout, stderr io.Writer) error {
		return acpcmd.RunGeminiACP(ctx, repoRoot, acpcmd.GeminiOptions{GeminiBin: wrapper}, input, stdout, stderr)
	})
}

func TestRunOpenCodeACPOneShot(t *testing.T) {
	wrapper, argsFile := writeOpenCodeWrapper(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := acpcmd.RunOpenCodeACP(context.Background(), t.TempDir(), acpcmd.OpenCodeOptions{
		Prompt:       "hello",
		Model:        "opencode/test-model",
		OpenCodeBin:  wrapper,
		OpenCodeArgs: []string{"--print-logs"},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runOpenCodeACP() error = %v", err)
	}

	if got := stdout.String(); got != sessionOneHelloResponse {
		t.Fatalf("stdout = %q, want %q", got, sessionOneHelloResponse)
	}
	if got := stderr.String(); !strings.Contains(got, "starting OpenCode ACP playground") {
		t.Fatalf("stderr = %q, want lifecycle log entry", got)
	}
	args := readArgsFile(t, argsFile)
	wantArgs := []string{"acp", "--print-logs"}
	for _, want := range wantArgs {
		if !containsArg(args, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
}

func TestRunOpenCodeACPReusesSessionInREPL(t *testing.T) {
	wrapper, _ := writeOpenCodeWrapper(t)
	testACPSessionReuseInREPL(t, func(ctx context.Context, repoRoot string, input io.Reader, stdout, stderr io.Writer) error {
		return acpcmd.RunOpenCodeACP(ctx, repoRoot, acpcmd.OpenCodeOptions{OpenCodeBin: wrapper}, input, stdout, stderr)
	})
}

func TestRunCodexACPOneShot(t *testing.T) {
	wrapper, argsFile := writeCodexACPWrapper(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := acpcmd.RunCodexACP(context.Background(), t.TempDir(), acpcmd.CodexOptions{
		Prompt:    "hello",
		BridgeBin: wrapper,
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCodexACP() error = %v", err)
	}

	if got := stdout.String(); got != sessionOneHelloResponse {
		t.Fatalf("stdout = %q, want %q", got, sessionOneHelloResponse)
	}
	if got := stderr.String(); !strings.Contains(got, "starting Codex ACP playground") {
		t.Fatalf("stderr = %q, want lifecycle log entry", got)
	}
	args := readArgsFile(t, argsFile)
	wantArgs := []string{"--debug", "proxy", "codex-acp"}
	for _, want := range wantArgs {
		if !containsArg(args, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
}

func TestRunCodexACPReusesSessionInREPL(t *testing.T) {
	wrapper, _ := writeCodexACPWrapper(t)
	testACPSessionReuseInREPL(t, func(ctx context.Context, repoRoot string, input io.Reader, stdout, stderr io.Writer) error {
		return acpcmd.RunCodexACP(ctx, repoRoot, acpcmd.CodexOptions{BridgeBin: wrapper}, input, stdout, stderr)
	})
}

type acpREPLRunner func(ctx context.Context, repoRoot string, input io.Reader, stdout, stderr io.Writer) error

func testACPSessionReuseInREPL(t *testing.T, runner acpREPLRunner) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	input := strings.NewReader("first\nsecond\nquit\n")
	err := runner(context.Background(), t.TempDir(), input, &stdout, &stderr)
	if err != nil {
		t.Fatalf("ACP runner error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "session-1:first") {
		t.Fatalf("stdout = %q, want first response", got)
	}
	if !strings.Contains(got, "session-1:second") {
		t.Fatalf("stdout = %q, want second response", got)
	}
	if strings.Contains(got, "session-2") {
		t.Fatalf("stdout = %q, want single ACP session reuse", got)
	}
	if got := stderr.String(); !strings.Contains(got, "starting interactive REPL") {
		t.Fatalf("stderr = %q, want repl lifecycle log entry", got)
	}
}

func TestRunACPInfoHuman(t *testing.T) {
	tests := []struct {
		name string
		run  func(ctx context.Context, repoRoot string, stdout, stderr io.Writer) error
	}{
		{
			name: acpSubcommandGemini,
			run: func(ctx context.Context, repoRoot string, stdout, stderr io.Writer) error {
				wrapper, _ := writeGeminiWrapper(t)
				return acpcmd.RunGeminiACPInfo(ctx, repoRoot, acpcmd.GeminiOptions{GeminiBin: wrapper}, false, stdout, stderr)
			},
		},
		{
			name: acpSubcommandOpenCode,
			run: func(ctx context.Context, repoRoot string, stdout, stderr io.Writer) error {
				wrapper, _ := writeOpenCodeWrapper(t)
				return acpcmd.RunOpenCodeACPInfo(ctx, repoRoot, acpcmd.OpenCodeOptions{OpenCodeBin: wrapper}, false, stdout, stderr)
			},
		},
		{
			name: acpSubcommandCodex,
			run: func(ctx context.Context, repoRoot string, stdout, stderr io.Writer) error {
				wrapper, _ := writeCodexACPWrapper(t)
				return acpcmd.RunCodexACPInfo(ctx, repoRoot, acpcmd.CodexOptions{BridgeBin: wrapper}, false, stdout, stderr)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := tc.run(context.Background(), t.TempDir(), &stdout, &stderr); err != nil {
				t.Fatalf("run info error = %v", err)
			}

			out := stdout.String()
			for _, want := range []string{
				"Agent:",
				"Protocol: 1",
				"Capabilities:",
				"Auth methods (0):",
				"Session: session-1",
				"Session modes: unavailable",
				"Session models: unavailable",
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("stdout = %q, want substring %q", out, want)
				}
			}
		})
	}
}

func TestRunACPInfoJSON(t *testing.T) {
	wrapper, _ := writeGeminiWrapper(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := acpcmd.RunGeminiACPInfo(
		context.Background(),
		t.TempDir(),
		acpcmd.GeminiOptions{GeminiBin: wrapper},
		true,
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("RunGeminiACPInfo() error = %v", err)
	}

	var got struct {
		Command    []string `json:"command"`
		Initialize struct {
			ProtocolVersion int `json:"protocolVersion"`
		} `json:"initialize"`
		Session struct {
			SessionID string `json:"sessionId"`
		} `json:"session"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout.String())
	}
	if got.Initialize.ProtocolVersion != 1 {
		t.Fatalf("initialize.protocolVersion = %d, want 1", got.Initialize.ProtocolVersion)
	}
	if len(got.Command) == 0 {
		t.Fatalf("command must not be empty")
	}
	if got.Session.SessionID == "" {
		t.Fatalf("session.sessionId must not be empty")
	}
}

func TestRunCodexMCPServerHumanOutput(t *testing.T) {
	wrapper, argsFile := writeCodexMCPWrapper(t, "full")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runCodexMCPServer(context.Background(), t.TempDir(), codexMCPServerOptions{
		CodexBin:  wrapper,
		CodexArgs: []string{"--trace"},
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCodexMCPServer() error = %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"Server: playground-mcp-helper",
		"Tools (1):",
		"- echo: Echoes text input",
		"Prompts (1):",
		"- greet: Greets the provided name",
		"Resources (1):",
		"- file:///playground/info.txt: Playground info resource",
		"Resource templates (1):",
		"- file:///playground/{name}: Playground resource template",
		"ADK tools (1):",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
	if got := stderr.String(); !strings.Contains(got, "starting Codex MCP server playground") {
		t.Fatalf("stderr = %q, want lifecycle log entry", got)
	}

	args := readArgsFile(t, argsFile)
	for _, want := range []string{"mcp-server", "--trace"} {
		if !containsArg(args, want) {
			t.Fatalf("args %v do not contain %q", args, want)
		}
	}
}

func TestRunCodexMCPServerJSONOutput(t *testing.T) {
	wrapper, _ := writeCodexMCPWrapper(t, "full")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runCodexMCPServer(context.Background(), t.TempDir(), codexMCPServerOptions{
		CodexBin: wrapper,
		JSON:     true,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCodexMCPServer() error = %v", err)
	}

	var got codexMCPInspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout.String())
	}
	if got.Initialize == nil || got.Initialize.ServerInfo == nil {
		t.Fatalf("initialize/serverInfo missing in output: %+v", got)
	}
	if got.Initialize.ServerInfo.Name != "playground-mcp-helper" {
		t.Fatalf("serverInfo.name = %q, want %q", got.Initialize.ServerInfo.Name, "playground-mcp-helper")
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one echo tool", got.Tools)
	}
	if len(got.Prompts) != 1 || got.Prompts[0].Name != "greet" {
		t.Fatalf("prompts = %+v, want one greet prompt", got.Prompts)
	}
	if len(got.Resources) != 1 || got.Resources[0].URI != "file:///playground/info.txt" {
		t.Fatalf("resources = %+v, want one playground resource", got.Resources)
	}
	if len(got.ResourceTemplates) != 1 || got.ResourceTemplates[0].URITemplate != "file:///playground/{name}" {
		t.Fatalf("resourceTemplates = %+v, want one playground template", got.ResourceTemplates)
	}
	if !got.Support.Tools || !got.Support.Prompts || !got.Support.Resources || !got.Support.ResourceTemplates {
		t.Fatalf("support = %+v, want all true", got.Support)
	}
	if len(got.ADKTools) != 1 || got.ADKTools[0].Name != "echo" {
		t.Fatalf("adk tools = %+v, want one echo tool", got.ADKTools)
	}
}

func TestRunCodexMCPServerToolsOnlyMarksUnsupportedFeatures(t *testing.T) {
	wrapper, _ := writeCodexMCPWrapper(t, "tools-only")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runCodexMCPServer(context.Background(), t.TempDir(), codexMCPServerOptions{
		CodexBin: wrapper,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCodexMCPServer() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Tools (1):",
		"Prompts: unsupported",
		"Resources: unsupported",
		"Resource templates: unsupported",
		"ADK tools (1):",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestBuildCodexACPCommand(t *testing.T) {
	got, err := acpcmd.BuildCodexACPCommand(acpcmd.CodexOptions{
		BridgeBin: "/tmp/norma",
		Model:     "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("buildCodexACPCommand() error = %v", err)
	}
	want := []string{"/tmp/norma", "--debug", "proxy", "codex-acp", "--codex-model", "gpt-5.4"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildCodexACPCommand() = %v, want %v", got, want)
	}
}

func TestBuildCodexACPCommandWithAgentName(t *testing.T) {
	got, err := acpcmd.BuildCodexACPCommand(acpcmd.CodexOptions{
		BridgeBin: "/tmp/norma",
		Model:     "gpt-5.4",
		Name:      "team-codex",
	})
	if err != nil {
		t.Fatalf("buildCodexACPCommand() error = %v", err)
	}
	want := []string{"/tmp/norma", "--debug", "proxy", "codex-acp", "--codex-model", "gpt-5.4", "--name", "team-codex"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("buildCodexACPCommand() = %v, want %v", got, want)
	}
}

func writeGeminiWrapper(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	wrapperPath := filepath.Join(dir, "gemini-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
: > %s
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %s
done
exec env GO_WANT_PLAYGROUND_ACP_HELPER=1 %s -test.run=TestPlaygroundACPHelperProcess -- "$@"
`, shellQuote(argsFile), shellQuote(argsFile), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath, argsFile
}

func writeOpenCodeWrapper(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	wrapperPath := filepath.Join(dir, "opencode-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
: > %s
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %s
done
exec env GO_WANT_PLAYGROUND_ACP_HELPER=1 %s -test.run=TestPlaygroundACPHelperProcess -- "$@"
`, shellQuote(argsFile), shellQuote(argsFile), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath, argsFile
}

func writeCodexACPWrapper(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	wrapperPath := filepath.Join(dir, "codex-acp-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
: > %s
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %s
done
exec env GO_WANT_PLAYGROUND_ACP_HELPER=1 %s -test.run=TestPlaygroundACPHelperProcess -- "$@"
`, shellQuote(argsFile), shellQuote(argsFile), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath, argsFile
}

func writeCodexMCPWrapper(t *testing.T, mode string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	wrapperPath := filepath.Join(dir, "codex-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
: > %s
for arg in "$@"; do
  printf '%%s\n' "$arg" >> %s
done
exec env GO_WANT_PLAYGROUND_MCP_HELPER=1 PLAYGROUND_MCP_HELPER_MODE=%s %s -test.run=TestPlaygroundMCPHelperProcess -- "$@"
`, shellQuote(argsFile), shellQuote(argsFile), shellQuote(mode), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath, argsFile
}

func readArgsFile(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestPlaygroundACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PLAYGROUND_ACP_HELPER") != "1" {
		return
	}
	runPlaygroundACPHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func TestPlaygroundMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PLAYGROUND_MCP_HELPER") != "1" {
		return
	}
	mustHelper(runPlaygroundMCPHelper(context.Background(), os.Getenv("PLAYGROUND_MCP_HELPER_MODE")))
	os.Exit(0)
}

func runPlaygroundACPHelper(stdin *os.File, stdout *os.File) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	sessionCount := 0

	for scanner.Scan() {
		var msg helperEnvelope
		mustHelper(json.Unmarshal(scanner.Bytes(), &msg))
		switch msg.Method {
		case methodInitialize:
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(helperInitializeResponse{ProtocolVersion: 1})})
		case methodSessionNew:
			sessionCount++
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(helperNewSessionResponse{SessionID: fmt.Sprintf("session-%d", sessionCount)})})
		case methodSessionSetModel:
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(helperSetSessionModelResponse{})})
		case methodSessionPrompt:
			var req helperPromptRequest
			mustHelper(json.Unmarshal(msg.Params, &req))
			writeHelperUpdate(stdout, req.SessionID, req.SessionID+":")
			writeHelperUpdate(stdout, req.SessionID, req.Prompt[0].Text)
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(helperPromptResponse{StopReason: "end_turn"})})
		case methodSessionCancel:
		default:
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Error: &helperError{Code: -32601, Message: "unsupported"}})
		}
	}
}

func runPlaygroundMCPHelper(ctx context.Context, mode string) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "playground-mcp-helper", Version: "v1.0.0"}, nil)

	if mode == "codex-tools" {
		mcp.AddTool(server, &mcp.Tool{Name: "codex", Description: "Starts a codex thread"}, func(_ context.Context, _ *mcp.CallToolRequest, input playgroundCodexToolInput) (*mcp.CallToolResult, playgroundCodexToolOutput, error) {
			return nil, playgroundCodexToolOutput{
				ThreadID: "thread-test",
				Content:  "codex:" + input.Prompt,
			}, nil
		})
		mcp.AddTool(server, &mcp.Tool{Name: "codex-reply", Description: "Continues a codex thread"}, func(_ context.Context, _ *mcp.CallToolRequest, input playgroundCodexReplyInput) (*mcp.CallToolResult, playgroundCodexToolOutput, error) {
			return nil, playgroundCodexToolOutput{
				ThreadID: input.ThreadID,
				Content:  "reply:" + input.Prompt,
			}, nil
		})
		return server.Run(ctx, &mcp.StdioTransport{})
	}

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echoes text input"}, func(_ context.Context, _ *mcp.CallToolRequest, input playgroundEchoInput) (*mcp.CallToolResult, playgroundEchoOutput, error) {
		return nil, playgroundEchoOutput{Echo: input.Text}, nil
	})

	if mode != "tools-only" {
		server.AddPrompt(&mcp.Prompt{
			Name:        "greet",
			Description: "Greets the provided name",
			Arguments: []*mcp.PromptArgument{
				{Name: "name", Description: "Name to greet", Required: true},
			},
		}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			name := req.Params.Arguments["name"]
			return &mcp.GetPromptResult{
				Description: "Greeting prompt",
				Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "Hello " + name}},
				},
			}, nil
		})
		server.AddResource(&mcp.Resource{
			URI:         "file:///playground/info.txt",
			Name:        "playground-info",
			Description: "Playground info resource",
			MIMEType:    "text/plain",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      req.Params.URI,
						MIMEType: "text/plain",
						Text:     "playground resource",
					},
				},
			}, nil
		})
		server.AddResourceTemplate(&mcp.ResourceTemplate{
			Name:        "playground-template",
			URITemplate: "file:///playground/{name}",
			Description: "Playground resource template",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      req.Params.URI,
						MIMEType: "text/plain",
						Text:     "playground template resource",
					},
				},
			}, nil
		})
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

type helperEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *helperError    `json:"error,omitempty"`
}

type helperError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type helperInitializeResponse struct {
	ProtocolVersion int `json:"protocolVersion"`
}

type helperNewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type helperPromptResponse struct {
	StopReason string `json:"stopReason"`
}

type helperSetSessionModelResponse struct{}

type helperPromptRequest struct {
	SessionID string              `json:"sessionId"`
	Prompt    []helperContentPart `json:"prompt"`
}

type helperContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type helperSessionNotification struct {
	SessionID string              `json:"sessionId"`
	Update    helperSessionUpdate `json:"update"`
}

type helperSessionUpdate struct {
	SessionUpdate string             `json:"sessionUpdate"`
	Content       *helperContentPart `json:"content,omitempty"`
}

type playgroundEchoInput struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

type playgroundEchoOutput struct {
	Echo string `json:"echo" jsonschema:"echoed text"`
}

type playgroundCodexToolInput struct {
	Prompt string `json:"prompt" jsonschema:"prompt text"`
}

type playgroundCodexReplyInput struct {
	ThreadID string `json:"threadId" jsonschema:"thread id"`
	Prompt   string `json:"prompt"   jsonschema:"prompt text"`
}

type playgroundCodexToolOutput struct {
	ThreadID string `json:"threadId" jsonschema:"thread id"`
	Content  string `json:"content"  jsonschema:"assistant response"`
}

func writeHelperUpdate(stdout *os.File, sessionID, text string) {
	writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", Method: methodSessionUpdate, Params: mustHelperJSON(helperSessionNotification{
		SessionID: sessionID,
		Update:    helperSessionUpdate{SessionUpdate: updateAgentMessageChunk, Content: &helperContentPart{Type: "text", Text: text}},
	})})
}

func writeHelperEnvelope(stdout *os.File, env helperEnvelope) {
	mustHelper(json.NewEncoder(stdout).Encode(env))
}

func mustHelperJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	mustHelper(err)
	return data
}

func mustHelper(err error) {
	if err != nil {
		panic(err)
	}
}
