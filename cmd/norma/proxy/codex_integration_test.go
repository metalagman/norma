//go:build integration && codex

package proxycmd

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
	"github.com/metalagman/norma/internal/adk/acpagent"
	codexacp "github.com/metalagman/norma/internal/codex/acp"
)

const testTimeout = 45 * time.Second

func TestCodexACPProxyIntegration_InitializeAndNewSession(t *testing.T) {
	repoRoot := requireCodexEnvironment(t)
	normaBin := buildNormaBinary(t, repoRoot)

	client, stderr := newProxyACPClient(t, repoRoot, normaBin)
	initResp := mustInitialize(t, client, stderr)
	if initResp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		t.Fatalf("initialize protocol version = %d, want %d", initResp.ProtocolVersion, acp.ProtocolVersionNumber)
	}

	mustNewSession(t, client, stderr, repoRoot)
}

func TestCodexACPProxyIntegration_CustomName(t *testing.T) {
	repoRoot := requireCodexEnvironment(t)
	normaBin := buildNormaBinary(t, repoRoot)

	client, stderr := newProxyACPClient(t, repoRoot, normaBin, "--name", "team-codex")
	initResp := mustInitialize(t, client, stderr)
	if initResp.AgentInfo == nil {
		t.Fatal("initialize agentInfo is nil")
	}
	if initResp.AgentInfo.Name != "team-codex" {
		t.Fatalf("initialize agentInfo.name = %q, want %q", initResp.AgentInfo.Name, "team-codex")
	}
}

func TestCodexACPProxyIntegration_DefaultName(t *testing.T) {
	repoRoot := requireCodexEnvironment(t)
	normaBin := buildNormaBinary(t, repoRoot)

	client, stderr := newProxyACPClient(t, repoRoot, normaBin)
	initResp := mustInitialize(t, client, stderr)
	if initResp.AgentInfo == nil {
		t.Fatal("initialize agentInfo is nil")
	}
	if initResp.AgentInfo.Name != codexacp.DefaultAgentName {
		t.Fatalf("initialize agentInfo.name = %q, want %q", initResp.AgentInfo.Name, codexacp.DefaultAgentName)
	}
}

func TestCodexACPProxyIntegration_RejectsPositionalArgs(t *testing.T) {
	repoRoot := requireCodexEnvironment(t)
	normaBin := buildNormaBinary(t, repoRoot)

	client, stderr := newProxyACPClient(t, repoRoot, normaBin, "--", "--definitely-invalid-flag")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if _, err := client.Initialize(ctx); err == nil {
		failWithDetails(t, "initialize unexpectedly succeeded with rejected positional arg", nil, stderr.String())
	}

	if !strings.Contains(stderr.String(), "accepts 0 arg(s)") {
		failWithDetails(t, "proxy stderr does not indicate positional args are rejected", nil, stderr.String())
	}
}

func requireCodexEnvironment(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("codex binary not found in PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	helpCmd := exec.CommandContext(ctx, "codex", "mcp-server", "--help")
	var helpOut bytes.Buffer
	helpCmd.Stdout = &helpOut
	helpCmd.Stderr = &helpOut
	if err := helpCmd.Run(); err != nil {
		t.Fatalf("codex mcp-server --help failed: %v | output=%s", err, strings.TrimSpace(helpOut.String()))
	}

	return findRepoRoot(t)
}

func findRepoRoot(t *testing.T) string {
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
			t.Fatalf("could not locate repo root containing go.mod (started from %q)", dir)
		}
		dir = parent
	}
}

func buildNormaBinary(t *testing.T, repoRoot string) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "norma")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/norma")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build norma binary failed: %v | output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func newProxyACPClient(t *testing.T, repoRoot, normaBin string, args ...string) (*acpagent.Client, *bytes.Buffer) {
	t.Helper()

	command := []string{normaBin, "proxy", "codex-acp"}
	command = append(command, args...)

	var stderr bytes.Buffer
	client, err := acpagent.NewClient(context.Background(), acpagent.ClientConfig{
		Command:    command,
		WorkingDir: repoRoot,
		Stderr:     &stderr,
	})
	if err != nil {
		failWithDetails(t, "start proxy acp client failed", err, stderr.String())
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client, &stderr
}

func mustInitialize(t *testing.T, client *acpagent.Client, stderr *bytes.Buffer) acp.InitializeResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	resp, err := client.Initialize(ctx)
	if err != nil {
		failWithDetails(t, "initialize failed", err, stderr.String())
	}
	return resp
}

func mustNewSession(t *testing.T, client *acpagent.Client, stderr *bytes.Buffer, cwd string) acp.NewSessionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	resp, err := client.NewSession(ctx, cwd)
	if err != nil {
		failWithDetails(t, "session/new failed", err, stderr.String())
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		failWithDetails(t, "session/new returned empty session id", nil, stderr.String())
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
