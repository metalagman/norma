package acpdump

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog/log"
)

func TestRunSuppressesPeerDisconnectInfoByDefault(t *testing.T) {
	t.Setenv("GO_WANT_ACPDUMP_HELPER", "1")
	if err := logging.Init(); err != nil {
		t.Fatalf("logging.Init() error = %v", err)
	}

	var stdout bytes.Buffer
	stderr := &lockedBuffer{}
	err := Run(log.Logger.WithContext(context.Background()), RunConfig{
		Command:    acpDumpHelperCommand(),
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := stderr.String(); strings.Contains(got, "peer connection closed") {
		t.Fatalf("stderr contains peer disconnect noise: %q", got)
	}
}

func TestRunKeepsACPDiagnosticsOutOfForwardedStderrInDebug(t *testing.T) {
	t.Setenv("GO_WANT_ACPDUMP_HELPER", "1")
	if err := logging.Init(logging.WithLevel(logging.LevelDebug)); err != nil {
		t.Fatalf("logging.Init() error = %v", err)
	}

	var stdout bytes.Buffer
	stderr := &lockedBuffer{}
	err := Run(log.Logger.WithContext(context.Background()), RunConfig{
		Command:    acpDumpHelperCommand(),
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := stderr.String()
	if !strings.Contains(got, "inspecting ACP agent") {
		t.Fatalf("stderr = %q, want app debug message", got)
	}
	if strings.Contains(got, "acp process exited during close") || strings.Contains(got, "peer connection closed") {
		t.Fatalf("stderr = %q, want no ACP package diagnostics", got)
	}
}

func TestRunSendsEmptyMCPServersArrayWhenUnset(t *testing.T) {
	t.Setenv("GO_WANT_ACPDUMP_HELPER", "1")
	if err := logging.Init(); err != nil {
		t.Fatalf("logging.Init() error = %v", err)
	}

	var stdout bytes.Buffer
	stderr := &lockedBuffer{}
	err := Run(log.Logger.WithContext(context.Background()), RunConfig{
		Command:    acpDumpHelperCommand(),
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunInitializeFailureDoesNotWarnOnClose(t *testing.T) {
	t.Setenv("GO_WANT_ACPDUMP_HELPER", "1")
	t.Setenv("GO_ACPDUMP_HELPER_FAIL_INITIALIZE", "1")
	if err := logging.Init(); err != nil {
		t.Fatalf("logging.Init() error = %v", err)
	}

	var stdout bytes.Buffer
	stderr := &lockedBuffer{}
	err := Run(log.Logger.WithContext(context.Background()), RunConfig{
		Command:    acpDumpHelperCommand(),
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     stderr,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want initialize failure")
	}
	if !strings.Contains(err.Error(), "initialize acp client") {
		t.Fatalf("Run() error = %v, want initialize acp client context", err)
	}
	if got := stderr.String(); strings.Contains(got, "failed to close ACP client") {
		t.Fatalf("stderr contains unexpected close warning: %q", got)
	}
	if got := stderr.String(); strings.Contains(got, "acp process exited with error") {
		t.Fatalf("stderr contains unexpected process exit warning: %q", got)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestACPDumpHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACPDUMP_HELPER") != "1" {
		return
	}
	runACPDumpHelper(os.Stdin, os.Stdout)
	os.Exit(0)
}

func acpDumpHelperCommand() []string {
	return []string{os.Args[0], "-test.run=TestACPDumpHelperProcess", "--"}
}

func runACPDumpHelper(stdin io.Reader, stdout io.Writer) {
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req helperEnvelope
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			if os.Getenv("GO_ACPDUMP_HELPER_FAIL_INITIALIZE") == "1" {
				writeHelperError(stdout, req.ID, -32603, "helper initialize failure")
				continue
			}
			writeHelperResponse(stdout, req.ID, map[string]any{
				"protocolVersion": 1,
				"agentInfo": map[string]any{
					"name":    "acpdump-helper",
					"version": "1.0.0",
				},
				"agentCapabilities": map[string]any{
					"loadSession": false,
					"mcpCapabilities": map[string]any{
						"http": false,
						"sse":  false,
					},
					"promptCapabilities": map[string]any{
						"audio":           false,
						"image":           false,
						"embeddedContext": false,
					},
				},
				"authMethods": []any{},
			})
		case "session/new":
			var sessionReq struct {
				McpServers json.RawMessage `json:"mcpServers"`
			}
			if err := json.Unmarshal(req.Params, &sessionReq); err != nil {
				writeHelperError(stdout, req.ID, -32602, "Invalid params")
				continue
			}
			if !isJSONArray(sessionReq.McpServers) {
				writeHelperError(stdout, req.ID, -32602, "Invalid params")
				continue
			}
			writeHelperResponse(stdout, req.ID, map[string]any{
				"sessionId": "session-1",
			})
		case "session/set_model":
			writeHelperResponse(stdout, req.ID, map[string]any{})
		case "session/set_mode":
			writeHelperResponse(stdout, req.ID, map[string]any{})
		default:
			writeHelperError(stdout, req.ID, -32601, "Method not found")
		}
	}
}

type helperEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func writeHelperResponse(stdout io.Writer, id json.RawMessage, result any) {
	writeHelperEnvelope(stdout, map[string]any{
		"jsonrpc": "2.0",
		"id":      mustJSONRaw(id),
		"result":  result,
	})
}

func writeHelperError(stdout io.Writer, id json.RawMessage, code int, message string) {
	writeHelperEnvelope(stdout, map[string]any{
		"jsonrpc": "2.0",
		"id":      mustJSONRaw(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeHelperEnvelope(stdout io.Writer, env map[string]any) {
	b, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	b = append(b, '\n')
	if _, err := stdout.Write(b); err != nil {
		panic(err)
	}
}

func mustJSONRaw(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(v, &out); err != nil {
		panic(err)
	}
	return out
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '['
}
