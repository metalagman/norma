package pdca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/normahq/norma/internal/agents/pdca/contracts"
	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/normahq/norma/pkg/runtime/structuredagent"
)

const (
	stopReasonBudgetExceeded = "budget_exceeded"
	statusError              = "error"
)

func TestResolvedAgentForRoleReturnsConfig(t *testing.T) {
	t.Parallel()

	agents := map[string]config.AgentConfig{
		"agent-1": {
			Type:      "gemini_acp",
			GeminiACP: &agentconfig.ACPConfig{Model: "gemini-1.5-flash"},
		},
	}
	roleIDs := map[string]string{
		"plan": "agent-1",
	}

	got, err := resolvedAgentForRole(agents, roleIDs, "plan")
	if err != nil {
		t.Fatalf("resolvedAgentForRole returned error: %v", err)
	}
	if got.Type != "gemini_acp" {
		t.Fatalf("agent type = %q, want %q", got.Type, "gemini_acp")
	}
}

func TestResolvedAgentForRoleReturnsRoleSpecificError(t *testing.T) {
	t.Parallel()

	_, err := resolvedAgentForRole(map[string]config.AgentConfig{}, map[string]string{}, "act")
	if err == nil {
		t.Fatal("resolvedAgentForRole returned nil error, want error")
	}
	if !strings.Contains(err.Error(), `role "act"`) {
		t.Fatalf("error %q does not include missing role", err.Error())
	}
}

func TestAgentOutputWritersNoDebug(t *testing.T) {
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

func TestAgentOutputWritersDebug(t *testing.T) {
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

func TestApplyAgentResponseToTaskStateActPersistsOutput(t *testing.T) {
	t.Parallel()

	state := &contracts.TaskState{}
	resp := &contracts.RawAgentResponse{
		Status:     "ok",
		StopReason: "none",
		ActOutput:  []byte(`{"decision":"close"}`),
	}

	applyAgentResponseToTaskState(state, resp, RoleAct)

	if state.Act == nil {
		t.Fatalf("state.Act = nil, want persisted act output")
	}
	var actOutput struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(state.Act, &actOutput); err != nil {
		t.Fatalf("unmarshal act output: %v", err)
	}
	if actOutput.Decision != "close" {
		t.Fatalf("act decision = %q, want %q", actOutput.Decision, "close")
	}
}

func TestCoerceTaskStatePointerAndValue(t *testing.T) {
	t.Parallel()

	actJSON := []byte(`{"decision":"close"}`)
	original := &contracts.TaskState{
		Act: actJSON,
	}
	gotPtr := coerceTaskState(original)
	if gotPtr != original {
		t.Fatalf("coerceTaskState(pointer) should return same pointer")
	}

	value := contracts.TaskState{
		Act: []byte(`{"decision":"replan"}`),
	}
	gotVal := coerceTaskState(value)
	if gotVal == nil || gotVal.Act == nil {
		t.Fatalf("coerceTaskState(value) returned nil act")
	}
	var actOutput struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(gotVal.Act, &actOutput); err != nil {
		t.Fatalf("unmarshal act output: %v", err)
	}
	if actOutput.Decision != "replan" {
		t.Fatalf("coerceTaskState(value) decision = %q, want %q", actOutput.Decision, "replan")
	}
}

func TestCoerceTaskStateHandlesUnexpectedType(t *testing.T) {
	t.Parallel()

	got := coerceTaskState("unexpected")
	if got == nil {
		t.Fatalf("coerceTaskState(unexpected) returned nil")
	}
	if got.Plan != nil || got.Do != nil || got.Check != nil || got.Act != nil {
		t.Fatalf("coerceTaskState(unexpected) should return empty state")
	}
}

func TestCoerceTaskStateFromMap(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"act": map[string]any{
			"decision":  "continue",
			"rationale": "needs more work",
			"next": map[string]any{
				"recommended": true,
				"notes":       "run do again",
			},
		},
	}

	got := coerceTaskState(raw)
	if got == nil || got.Act == nil {
		t.Fatalf("coerceTaskState(map) returned nil act")
	}
	var actOutput struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(got.Act, &actOutput); err != nil {
		t.Fatalf("unmarshal act output: %v", err)
	}
	if actOutput.Decision != "continue" {
		t.Fatalf("coerceTaskState(map) decision = %q, want %q", actOutput.Decision, "continue")
	}
}

func TestValidateStepResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		resp    *contracts.RawAgentResponse
		wantErr bool
	}{
		{
			name: "plan ok with payload",
			role: RolePlan,
			resp: &contracts.RawAgentResponse{
				Status:     "ok",
				PlanOutput: []byte(`{}`),
			},
			wantErr: false,
		},
		{
			name: "plan ok missing payload",
			role: RolePlan,
			resp: &contracts.RawAgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "plan stop without payload",
			role: RolePlan,
			resp: &contracts.RawAgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "plan error status",
			role: RolePlan,
			resp: &contracts.RawAgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "do ok with payload",
			role: RoleDo,
			resp: &contracts.RawAgentResponse{
				Status:   "ok",
				DoOutput: []byte(`{}`),
			},
			wantErr: false,
		},
		{
			name: "do ok missing payload",
			role: RoleDo,
			resp: &contracts.RawAgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "do stop without payload",
			role: RoleDo,
			resp: &contracts.RawAgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "do error status",
			role: RoleDo,
			resp: &contracts.RawAgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "check ok with payload",
			role: RoleCheck,
			resp: &contracts.RawAgentResponse{
				Status:      "ok",
				CheckOutput: []byte(`{}`),
			},
			wantErr: false,
		},
		{
			name: "check ok missing payload",
			role: RoleCheck,
			resp: &contracts.RawAgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "check error status",
			role: RoleCheck,
			resp: &contracts.RawAgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "act ok with payload",
			role: RoleAct,
			resp: &contracts.RawAgentResponse{
				Status:    "ok",
				ActOutput: []byte(`{}`),
			},
			wantErr: false,
		},
		{
			name: "act ok missing payload",
			role: RoleAct,
			resp: &contracts.RawAgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "unknown role",
			role: "unknown",
			resp: &contracts.RawAgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name:    "nil response",
			role:    RolePlan,
			resp:    nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateStepResponse(tc.role, tc.resp)
			if tc.wantErr && err == nil {
				t.Fatalf("validateStepResponse() expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateStepResponse() unexpected error: %v", err)
			}
		})
	}
}

func TestStepFailureStopReason(t *testing.T) {
	t.Parallel()

	if got := stepFailureStopReason(context.DeadlineExceeded); got != stopReasonBudgetExceeded {
		t.Fatalf("stepFailureStopReason(deadline) = %q, want %q", got, stopReasonBudgetExceeded)
	}

	schemaErr := fmt.Errorf("outer: %w", structuredagent.ErrStructuredOutputSchemaValidation)
	if got := stepFailureStopReason(schemaErr); got != stopReasonReplanRequired {
		t.Fatalf("stepFailureStopReason(schema) = %q, want %q", got, stopReasonReplanRequired)
	}

	blockedErr := errors.New("dependency blocked waiting for upstream")
	if got := stepFailureStopReason(blockedErr); got != "dependency_blocked" {
		t.Fatalf("stepFailureStopReason(blocked) = %q, want %q", got, "dependency_blocked")
	}

	otherErr := errors.New("unexpected failure")
	if got := stepFailureStopReason(otherErr); got != stopReasonReplanRequired {
		t.Fatalf("stepFailureStopReason(other) = %q, want %q", got, "replan_required")
	}
}

func TestStepFailureResponseIncludesSummaryAndExitCode(t *testing.T) {
	t.Parallel()

	resp := stepFailureResponse(RoleCheck, errors.New("structured output mismatch"), 2)
	if resp == nil {
		t.Fatal("stepFailureResponse() returned nil response")
	}
	if resp.Status != statusError {
		t.Fatalf("resp.Status = %q, want %q", resp.Status, "error")
	}
	if !strings.Contains(resp.Summary, "exit_code=2") {
		t.Fatalf("resp.Summary = %q, want contains %q", resp.Summary, "exit_code=2")
	}
}

func TestNewLoopAgentRegistersRoleSubAgents(t *testing.T) {
	t.Parallel()

	loopAgent, err := NewLoopAgent(
		context.Background(),
		pdcaTestConfig(),
		nil,
		nil,
		AgentInput{WorkingDir: t.TempDir()},
		"",
		3,
	)
	if err != nil {
		t.Fatalf("NewLoopAgent() error = %v", err)
	}

	subAgents := loopAgent.SubAgents()
	if len(subAgents) != 4 {
		t.Fatalf("len(loopAgent.SubAgents()) = %d, want 4", len(subAgents))
	}

	gotNames := make([]string, 0, len(subAgents))
	for _, subAgent := range subAgents {
		gotNames = append(gotNames, subAgent.Name())
	}
	wantNames := []string{"Plan", "Do", "Check", "Act"}
	for _, want := range wantNames {
		if !slices.Contains(gotNames, want) {
			t.Fatalf("missing subagent %q, got %v", want, gotNames)
		}
	}
}

func TestRuntimeSharedWorkspaceUsesRunScopedTaskBranchWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	initTestRepo(t, ctx, workingDir)
	writeTestFile(t, filepath.Join(workingDir, "base.txt"), "base\n")
	runGit(t, ctx, workingDir, "add", "base.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: seed repo")

	runDir := filepath.Join(workingDir, ".norma", "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	rt := &runtime{
		runInput: AgentInput{
			RunDir:     runDir,
			WorkingDir: workingDir,
			TaskID:     "norma-shared",
		},
	}

	got, err := rt.sharedWorkspace(ctx)
	if err != nil {
		t.Fatalf("sharedWorkspace() error = %v", err)
	}
	want := filepath.Join(runDir, "workspace")
	if got != want {
		t.Fatalf("shared workspace = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(got, "base.txt")); err != nil {
		t.Fatalf("shared workspace missing base file: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, ctx, got, "branch", "--show-current")); branch != "norma/task/norma-shared" {
		t.Fatalf("workspace branch = %q, want %q", branch, "norma/task/norma-shared")
	}

	again, err := rt.sharedWorkspace(ctx)
	if err != nil {
		t.Fatalf("sharedWorkspace() second call error = %v", err)
	}
	if again != got {
		t.Fatalf("shared workspace second call = %q, want %q", again, got)
	}
}

func pdcaTestConfig() config.Config {
	agentID := "agent-1"
	return config.Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]config.AgentConfig{
				agentID: {
					Type: config.AgentTypeGenericACP,
					GenericACP: &agentconfig.ACPConfig{
						Cmd: []string{"custom-acp", "--stdio"},
					},
				},
			},
		},
		RoleIDs: map[string]string{
			RolePlan:  agentID,
			RoleDo:    agentID,
			RoleCheck: agentID,
			RoleAct:   agentID,
		},
	}
}

func TestCommitWorkspaceChangesCommitsDirtyWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	initTestRepo(t, ctx, workingDir)

	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\n")
	runGit(t, ctx, workingDir, "add", "a.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, workingDir, "rev-parse", "HEAD"))

	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\ntwo\n")
	writeTestFile(t, filepath.Join(workingDir, "b.txt"), "new\n")

	if err := commitWorkspaceChanges(ctx, workingDir, "run-1", "norma-8sl", 2); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, workingDir, "rev-parse", "HEAD"))
	if after == before {
		t.Fatalf("expected a new commit, HEAD unchanged at %s", after)
	}

	commitMsg := runGit(t, ctx, workingDir, "log", "-1", "--pretty=%B")
	if !strings.Contains(commitMsg, "chore: do step 002") {
		t.Fatalf("commit message missing step info:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Run: run-1") {
		t.Fatalf("commit message missing run id:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Task: norma-8sl") {
		t.Fatalf("commit message missing task id:\n%s", commitMsg)
	}

	status := strings.TrimSpace(runGit(t, ctx, workingDir, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected clean workspace after commit, got:\n%s", status)
	}
}

func TestCommitWorkspaceChangesNoopForCleanWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	initTestRepo(t, ctx, workingDir)

	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\n")
	runGit(t, ctx, workingDir, "add", "a.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, workingDir, "rev-parse", "HEAD"))

	if err := commitWorkspaceChanges(ctx, workingDir, "run-2", "norma-8sl", 3); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, workingDir, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("expected no commit for clean workspace; before=%s after=%s", before, after)
	}
}

func TestCommitWorkspaceChangesReturnsErrorWhenStatusFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	nonRepoDir := t.TempDir()

	err := commitWorkspaceChanges(ctx, nonRepoDir, "run-3", "norma-8sl", 4)
	if err == nil {
		t.Fatal("commitWorkspaceChanges() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read workspace status") {
		t.Fatalf("error = %q, want read workspace status context", err)
	}
}

func TestEnsureWorkspaceCleanAcceptsCleanWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	initTestRepo(t, ctx, workingDir)

	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\n")
	runGit(t, ctx, workingDir, "add", "a.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: initial")

	if err := ensureWorkspaceClean(ctx, workingDir, RoleCheck); err != nil {
		t.Fatalf("ensureWorkspaceClean() error = %v", err)
	}
}

func TestEnsureWorkspaceCleanRejectsDirtyWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workingDir := t.TempDir()
	initTestRepo(t, ctx, workingDir)

	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\n")
	runGit(t, ctx, workingDir, "add", "a.txt")
	runGit(t, ctx, workingDir, "commit", "-m", "chore: initial")
	writeTestFile(t, filepath.Join(workingDir, "a.txt"), "one\ntwo\n")

	err := ensureWorkspaceClean(ctx, workingDir, RoleAct)
	if err == nil {
		t.Fatal("ensureWorkspaceClean() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "only do may leave workspace changes") {
		t.Fatalf("error = %q, want role mutation guard", err)
	}
}

func initTestRepo(t *testing.T, ctx context.Context, workingDir string) {
	t.Helper()
	runGit(t, ctx, workingDir, "init")
	runGit(t, ctx, workingDir, "config", "user.name", "Norma Test")
	runGit(t, ctx, workingDir, "config", "user.email", "norma-test@example.com")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func runGit(t *testing.T, ctx context.Context, workingDir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workingDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
