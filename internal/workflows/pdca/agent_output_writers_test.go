package pdca

import (
	"bytes"
	"testing"
)

func TestAgentOutputWriters_NoDebug(t *testing.T) {
	t.Parallel()

	var stdoutLog bytes.Buffer
	var stderrLog bytes.Buffer
	stdout, stderr := agentOutputWriters(false, &stdoutLog, &stderrLog)

	if stdout != &stdoutLog {
		t.Fatalf("stdout writer should be log-only writer when debug is disabled")
	}
	if stderr != &stderrLog {
		t.Fatalf("stderr writer should be log-only writer when debug is disabled")
	}
}

func TestAgentOutputWriters_Debug(t *testing.T) {
	t.Parallel()

	var stdoutLog bytes.Buffer
	var stderrLog bytes.Buffer
	stdout, stderr := agentOutputWriters(true, &stdoutLog, &stderrLog)

	if stdout == &stdoutLog {
		t.Fatalf("stdout writer should include console + log writer when debug is enabled")
	}
	if stderr == &stderrLog {
		t.Fatalf("stderr writer should include console + log writer when debug is enabled")
	}

	if _, err := stdout.Write([]byte("out")); err != nil {
		t.Fatalf("write debug stdout: %v", err)
	}
	if _, err := stderr.Write([]byte("err")); err != nil {
		t.Fatalf("write debug stderr: %v", err)
	}
	if stdoutLog.String() != "out" {
		t.Fatalf("stdout log captured %q, want %q", stdoutLog.String(), "out")
	}
	if stderrLog.String() != "err" {
		t.Fatalf("stderr log captured %q, want %q", stderrLog.String(), "err")
	}
}
