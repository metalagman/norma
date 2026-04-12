//go:build integration && codex

package codexacp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/pkg/runtime/acpagent"
)

const integrationTestTimeout = 60 * time.Second

func TestCodexACPIntegrationInitializeAndNewSession(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	initResp := mustInitialize(t, client, stderr)
	if initResp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		t.Fatalf("initialize protocol version = %d, want %d", initResp.ProtocolVersion, acp.ProtocolVersionNumber)
	}

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	resp, err := client.NewSession(ctx, workingDir, nil)
	if err != nil {
		failWithDetails(t, "session/new failed", err, stderr.String())
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		failWithDetails(t, "session/new returned empty session id", nil, stderr.String())
	}
}

func TestCodexACPIntegrationBasicPromptFlow(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	sessionResp, err := client.NewSession(ctx, workingDir, nil)
	if err != nil {
		failWithDetails(t, "session/new failed", err, stderr.String())
	}

	updates, resultCh, err := client.Prompt(ctx, string(sessionResp.SessionId), "Say only: ok")
	if err != nil {
		failWithDetails(t, "session/prompt failed to start", err, stderr.String())
	}

	promptResult := awaitPromptResult(ctx, updates, resultCh)
	if promptResult.Err != nil {
		if isLikelyAuthEnvError(promptResult.Err) {
			t.Skipf("skipping prompt integration without codex auth/session context: %v", promptResult.Err)
		}
		failWithDetails(t, "session/prompt failed", promptResult.Err, stderr.String())
	}

	if promptResult.Response.StopReason == "" {
		t.Fatal("prompt response stop reason is empty")
	}
}

func awaitPromptResult(
	ctx context.Context,
	updates <-chan acpagent.ExtendedSessionNotification,
	resultCh <-chan acpagent.PromptResult,
) acpagent.PromptResult {
	var result acpagent.PromptResult
	for updates != nil || resultCh != nil {
		select {
		case <-ctx.Done():
			return acpagent.PromptResult{Err: ctx.Err()}
		case _, ok := <-updates:
			if !ok {
				updates = nil
			}
		case r, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			result = r
			resultCh = nil
		}
	}
	return result
}

func isLikelyAuthEnvError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	hints := []string{
		"unauthorized",
		"auth",
		"login",
		"api key",
		"account",
		"forbidden",
		"401",
		"403",
	}
	for _, hint := range hints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func requireCodexEnvironment(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("codex binary not found in PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	helpCmd := exec.CommandContext(ctx, "codex", "app-server", "--help")
	var helpOut bytes.Buffer
	helpCmd.Stdout = &helpOut
	helpCmd.Stderr = &helpOut
	if err := helpCmd.Run(); err != nil {
		t.Fatalf("codex app-server --help failed: %v | output=%s", err, strings.TrimSpace(helpOut.String()))
	}

	return findWorkingDir(t)
}

func findWorkingDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat go.mod failed in %q: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate working dir containing go.mod (started from %q)", dir)
		}
		dir = parent
	}
}

func buildCodexACPBinary(t *testing.T, workingDir string) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "codex-acp-app-server")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/codex-acp-app-server")
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build codex-acp-app-server binary failed: %v | output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func newCodexACPClient(t *testing.T, workingDir, binPath string, args ...string) (*acpagent.Client, *bytes.Buffer) {
	t.Helper()

	command := []string{binPath}
	command = append(command, args...)

	var stderr bytes.Buffer
	client, err := acpagent.NewClient(context.Background(), acpagent.ClientConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     &stderr,
	})
	if err != nil {
		failWithDetails(t, "start codex-acp-app-server client failed", err, stderr.String())
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client, &stderr
}

func mustInitialize(t *testing.T, client *acpagent.Client, stderr *bytes.Buffer) acp.InitializeResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	resp, err := client.Initialize(ctx)
	if err != nil {
		failWithDetails(t, "initialize failed", err, stderr.String())
	}
	return resp
}

func failWithDetails(t *testing.T, heading string, err error, stderr string) {
	t.Helper()

	errText := ""
	if err != nil {
		errText = strings.TrimSpace(err.Error())
	}
	stderrText := strings.TrimSpace(stderr)

	message := heading
	if errText != "" {
		message += ": " + errText
	}
	if stderrText != "" && (errText == "" || !strings.Contains(stderrText, errText)) {
		message += " | stderr: " + stderrText
	}
	t.Fatal(message)
}
