package goalkeeper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

const wantCompletedStatus = "completed"

func TestBuildCodexACPCommand(t *testing.T) {
	t.Parallel()

	got := BuildCodexACPCommand("")
	want := []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildCodexACPCommand(\"\") = %v, want %v", got, want)
	}

	got = BuildCodexACPCommand("/tmp/bridge")
	if len(got) != 1 || got[0] != "/tmp/bridge" {
		t.Fatalf("BuildCodexACPCommand(custom) = %v, want custom binary only", got)
	}
}

func TestFixedACPConfig(t *testing.T) {
	t.Parallel()

	if defaultAgentType != "codex_acp" {
		t.Fatalf("defaultAgentType = %q, want codex_acp", defaultAgentType)
	}
	if defaultModel != "gpt-5.3-codex" {
		t.Fatalf("defaultModel = %q, want gpt-5.3-codex", defaultModel)
	}
}

func TestRunJobToolDispatchesRole(t *testing.T) {
	t.Parallel()

	runner := &fakeJobRunner{result: "planned"}
	var logs bytes.Buffer
	logger := zerolog.New(&logs).Level(zerolog.DebugLevel)
	svc := newService(runner, logger, 2)

	_, out, err := svc.runJob(context.Background(), nil, runJobInput{
		JobID: "job-plan",
		Role:  "plan",
		Task:  "Plan the goal",
	})
	if err != nil {
		t.Fatalf("runJob() error = %v", err)
	}
	if out.Status != wantCompletedStatus {
		t.Fatalf("out.Status = %q, want completed", out.Status)
	}
	if out.Result != "planned" {
		t.Fatalf("out.Result = %q, want planned", out.Result)
	}
	if runner.role != "plan" || runner.task != "Plan the goal" {
		t.Fatalf("runner got role=%q task=%q", runner.role, runner.task)
	}
	gotLogs := logs.String()
	if !strings.Contains(gotLogs, `"level":"debug"`) ||
		!strings.Contains(gotLogs, `"job_id":"job-plan"`) ||
		!strings.Contains(gotLogs, `"message":"job dispatch"`) ||
		!strings.Contains(gotLogs, `"message":"job completed"`) {
		t.Fatalf("logs = %q, want debug dispatch and completion entries", gotLogs)
	}
}

func TestRunJobToolSuppressesJobLogsAboveDebug(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := zerolog.New(&logs).Level(zerolog.InfoLevel)
	svc := newService(&fakeJobRunner{result: "planned"}, logger, 1)

	_, out, err := svc.runJob(context.Background(), nil, runJobInput{
		JobID: "job-plan",
		Role:  "plan",
		Task:  "Plan the goal",
	})
	if err != nil {
		t.Fatalf("runJob() error = %v", err)
	}
	if out.Status != wantCompletedStatus {
		t.Fatalf("out.Status = %q, want completed", out.Status)
	}
	if got := logs.String(); got != "" {
		t.Fatalf("logs = %q, want no job logs above debug", got)
	}
}

func TestRunJobToolValidation(t *testing.T) {
	t.Parallel()

	svc := newService(&fakeJobRunner{}, zerolog.Nop(), 1)
	result, out, err := svc.runJob(context.Background(), nil, runJobInput{JobID: "job-x", Role: "invalid", Task: "x"})
	if err != nil {
		t.Fatalf("runJob() error = %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("result.IsError = false, want true")
	}
	if out.Status != "error" {
		t.Fatalf("out.Status = %q, want error", out.Status)
	}
}

func TestRunJobToolMaxToolCalls(t *testing.T) {
	t.Parallel()

	svc := newService(&fakeJobRunner{result: "ok"}, zerolog.Nop(), 1)
	_, out, err := svc.runJob(context.Background(), nil, runJobInput{JobID: "job-1", Role: "plan", Task: "x"})
	if err != nil || out.Status != wantCompletedStatus {
		t.Fatalf("first runJob() out=%+v err=%v, want completed", out, err)
	}
	result, out, err := svc.runJob(context.Background(), nil, runJobInput{JobID: "job-2", Role: "do", Task: "x"})
	if err != nil {
		t.Fatalf("second runJob() error = %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("second result.IsError = false, want true")
	}
	if out.Result != "max tool calls exceeded" {
		t.Fatalf("out.Result = %q, want max tool calls exceeded", out.Result)
	}
}

func TestRunJobToolRunnerError(t *testing.T) {
	t.Parallel()

	svc := newService(&fakeJobRunner{err: errors.New("role failed")}, zerolog.Nop(), 1)
	result, out, err := svc.runJob(context.Background(), nil, runJobInput{JobID: "job-1", Role: "check", Task: "x"})
	if err != nil {
		t.Fatalf("runJob() error = %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("result.IsError = false, want true")
	}
	if out.Result != "role failed" {
		t.Fatalf("out.Result = %q, want role failed", out.Result)
	}
}

func TestRunWithFakeACPBridge(t *testing.T) {
	wrapper := writeACPWrapper(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var logs bytes.Buffer
	logger := zerolog.New(&logs).Level(zerolog.DebugLevel)

	err := Run(context.Background(), Config{
		Goal:         "test goal",
		WorkingDir:   t.TempDir(),
		BridgeBin:    wrapper,
		MaxToolCalls: 1,
		Stdout:       &stdout,
		Stderr:       &stderr,
		Logger:       &logger,
	})
	if err != nil {
		t.Fatalf("Run() error = %v; stderr=%s", err, stderr.String())
	}

	got := stdout.String()
	if strings.Contains(got, "scheduler:") || strings.Contains(got, "job job-") {
		t.Fatalf("stdout = %q, want only final command output", got)
	}
	if !strings.Contains(got, "GOAL JOB:\ntest goal") {
		t.Fatalf("stdout = %q, want goal in scheduler response", got)
	}
	gotLogs := logs.String()
	if !strings.Contains(gotLogs, `"message":"scheduler started"`) ||
		!strings.Contains(gotLogs, `"message":"scheduler completed"`) {
		t.Fatalf("logs = %q, want scheduler lifecycle entries", gotLogs)
	}
}

type fakeJobRunner struct {
	role   string
	task   string
	result string
	err    error
}

func (r *fakeJobRunner) RunJob(_ context.Context, role string, task string) (string, error) {
	r.role = role
	r.task = task
	return r.result, r.err
}

func writeACPWrapper(t *testing.T) string {
	t.Helper()
	wrapperPath := filepath.Join(t.TempDir(), "codex-acp-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
exec env GO_WANT_GOALKEEPER_ACP_HELPER=1 %s -test.run=TestGoalkeeperACPHelperProcess -- "$@"
`, shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestGoalkeeperACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALKEEPER_ACP_HELPER") != "1" {
		return
	}
	runGoalkeeperACPHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func runGoalkeeperACPHelper(stdin *os.File, stdout *os.File) {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	sessionCount := 0

	for scanner.Scan() {
		var msg helperEnvelope
		mustHelper(json.Unmarshal(scanner.Bytes(), &msg))
		switch msg.Method {
		case "initialize":
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(map[string]any{"protocolVersion": 1})})
		case "session/new":
			sessionCount++
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(map[string]any{"sessionId": fmt.Sprintf("session-%d", sessionCount)})})
		case "session/set_model":
			if !strings.Contains(string(msg.Params), defaultModel) {
				writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Error: &helperError{Code: -32602, Message: "unexpected model"}})
				continue
			}
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(map[string]any{})})
		case "session/prompt":
			var req helperPromptRequest
			mustHelper(json.Unmarshal(msg.Params, &req))
			writeHelperUpdate(stdout, req.SessionID, req.Prompt[0].Text)
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Result: mustHelperJSON(map[string]any{"stopReason": "end_turn"})})
		case "session/cancel":
		default:
			writeHelperEnvelope(stdout, helperEnvelope{JSONRPC: "2.0", ID: msg.ID, Error: &helperError{Code: -32601, Message: "unsupported"}})
		}
	}
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

type helperPromptRequest struct {
	SessionID string `json:"sessionId"`
	Prompt    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"prompt"`
}

type helperUpdate struct {
	SessionID string           `json:"sessionId"`
	Update    helperUpdateBody `json:"update"`
}

type helperUpdateBody struct {
	SessionUpdate string `json:"sessionUpdate"`
	Content       struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func mustHelper(err error) {
	if err != nil {
		panic(err)
	}
}

func mustHelperJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	mustHelper(err)
	return data
}

func writeHelperEnvelope(stdout *os.File, msg helperEnvelope) {
	data, err := json.Marshal(msg)
	mustHelper(err)
	_, err = stdout.Write(append(data, '\n'))
	mustHelper(err)
}

func writeHelperUpdate(stdout *os.File, sessionID string, text string) {
	update := helperUpdate{SessionID: sessionID}
	update.Update.SessionUpdate = "agent_message_chunk"
	update.Update.Content.Type = "text"
	update.Update.Content.Text = text
	writeHelperEnvelope(stdout, helperEnvelope{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  mustHelperJSON(update),
	})
}
