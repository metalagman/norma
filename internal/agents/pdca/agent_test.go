package pdca

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles/act"
	"github.com/metalagman/norma/internal/agents/pdca/roles/check"
	"github.com/metalagman/norma/internal/agents/pdca/roles/do"
	"github.com/metalagman/norma/internal/agents/pdca/roles/plan"
	"github.com/metalagman/norma/internal/config"
)

func TestResolvedAgentForRoleReturnsConfig(t *testing.T) {
	t.Parallel()

	agents := map[string]config.AgentConfig{
		"plan": {Type: "codex", Model: "gpt-5.2-codex"},
	}

	got, err := resolvedAgentForRole(agents, "plan")
	if err != nil {
		t.Fatalf("resolvedAgentForRole returned error: %v", err)
	}
	if got.Type != "codex" {
		t.Fatalf("agent type = %q, want %q", got.Type, "codex")
	}
}

func TestResolvedAgentForRoleReturnsRoleSpecificError(t *testing.T) {
	t.Parallel()

	_, err := resolvedAgentForRole(map[string]config.AgentConfig{}, "act")
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

func TestApplyAgentResponseToTaskStateActPersistsOutputAndJournal(t *testing.T) {
	t.Parallel()

	state := &contracts.TaskState{}
	resp := &contracts.AgentResponse{
		Status:     "ok",
		StopReason: "none",
		Progress: contracts.StepProgress{
			Title:   "Act decision applied",
			Details: []string{"Decision close"},
		},
		Act: &act.ActOutput{
			Decision: "close",
		},
	}

	ts := time.Date(2026, time.February, 12, 13, 14, 15, 0, time.UTC)
	applyAgentResponseToTaskState(state, resp, RoleAct, "run-1", 2, 4, ts)

	if state.Act == nil {
		t.Fatalf("state.Act = nil, want persisted act output")
	}
	if state.Act.Decision != "close" {
		t.Fatalf("state.Act.Decision = %q, want %q", state.Act.Decision, "close")
	}

	if len(state.Journal) != 1 {
		t.Fatalf("len(state.Journal) = %d, want 1", len(state.Journal))
	}
	entry := state.Journal[0]
	if entry.Role != RoleAct {
		t.Fatalf("journal role = %q, want %q", entry.Role, RoleAct)
	}
	if entry.StepIndex != 4 {
		t.Fatalf("journal step index = %d, want 4", entry.StepIndex)
	}
	if entry.RunID != "run-1" {
		t.Fatalf("journal run id = %q, want %q", entry.RunID, "run-1")
	}
	if entry.Iteration != 2 {
		t.Fatalf("journal iteration = %d, want %d", entry.Iteration, 2)
	}
	if entry.Title != "Act decision applied" {
		t.Fatalf("journal title = %q, want %q", entry.Title, "Act decision applied")
	}
	if entry.Timestamp != "2026-02-12T13:14:15Z" {
		t.Fatalf("journal timestamp = %q, want %q", entry.Timestamp, "2026-02-12T13:14:15Z")
	}
}

func TestApplyAgentResponseToTaskStateDefaultsJournalTitle(t *testing.T) {
	t.Parallel()

	state := &contracts.TaskState{}
	resp := &contracts.AgentResponse{
		Status:     "ok",
		StopReason: "none",
		Progress: contracts.StepProgress{
			Details: []string{"no explicit title"},
		},
		Act: &act.ActOutput{
			Decision: "replan",
		},
	}

	ts := time.Date(2026, time.February, 12, 13, 14, 15, 0, time.UTC)
	applyAgentResponseToTaskState(state, resp, RoleAct, "run-2", 3, 5, ts)

	if len(state.Journal) != 1 {
		t.Fatalf("len(state.Journal) = %d, want 1", len(state.Journal))
	}
	if state.Journal[0].Title != "act step completed" {
		t.Fatalf("journal title = %q, want %q", state.Journal[0].Title, "act step completed")
	}
}

func TestReconstructProgressIncludesTaskRunAndIteration(t *testing.T) {
	t.Parallel()

	agent := &runtime{
		runInput: AgentInput{
			TaskID: "norma-95b",
			RunID:  "run-default",
		},
	}

	stepDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stepDir, "artifacts"), 0o755); err != nil {
		t.Fatalf("create artifacts dir: %v", err)
	}

	state := &contracts.TaskState{
		Journal: []contracts.JournalEntry{
			{
				Timestamp:  "2026-02-12T10:00:00Z",
				RunID:      "run-abc",
				Iteration:  7,
				StepIndex:  3,
				Role:       "do",
				Status:     "ok",
				StopReason: "none",
				Title:      "Executed planned changes",
				Details:    []string{"updated files", "ran tests"},
			},
		},
	}

	if err := agent.reconstructProgress(stepDir, state); err != nil {
		t.Fatalf("reconstructProgress() error = %v", err)
	}

	contentBytes, err := os.ReadFile(filepath.Join(stepDir, "artifacts", "progress.md"))
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "**Task:** norma-95b") {
		t.Fatalf("progress missing task line:\n%s", content)
	}
	if !strings.Contains(content, "**Run:** run-abc · **Iteration:** 7") {
		t.Fatalf("progress missing run/iteration line:\n%s", content)
	}
	if !strings.Contains(content, "## 2026-02-12T10:00:00Z — 3 DO — ok/none") {
		t.Fatalf("progress missing header line:\n%s", content)
	}
	if !strings.Contains(content, "- updated files") || !strings.Contains(content, "- ran tests") {
		t.Fatalf("progress missing details bullets:\n%s", content)
	}
}

func TestCoerceTaskStatePointerAndValue(t *testing.T) {
	t.Parallel()

	original := &contracts.TaskState{
		Act: &act.ActOutput{Decision: "close"},
	}
	gotPtr := coerceTaskState(original)
	if gotPtr != original {
		t.Fatalf("coerceTaskState(pointer) should return same pointer")
	}

	value := contracts.TaskState{
		Act: &act.ActOutput{Decision: "replan"},
	}
	gotVal := coerceTaskState(value)
	if gotVal == nil || gotVal.Act == nil {
		t.Fatalf("coerceTaskState(value) returned nil act")
	}
	if gotVal.Act.Decision != "replan" {
		t.Fatalf("coerceTaskState(value) decision = %q, want %q", gotVal.Act.Decision, "replan")
	}
}

func TestCoerceTaskStateHandlesUnexpectedType(t *testing.T) {
	t.Parallel()

	got := coerceTaskState("unexpected")
	if got == nil {
		t.Fatalf("coerceTaskState(unexpected) returned nil")
	}
	if got.Plan != nil || got.Do != nil || got.Check != nil || got.Act != nil || len(got.Journal) != 0 {
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
	if got.Act.Decision != "continue" {
		t.Fatalf("coerceTaskState(map) decision = %q, want %q", got.Act.Decision, "continue")
	}
}



func TestValidateStepResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		resp    *contracts.AgentResponse
		wantErr bool
	}{
		{
			name: "plan ok with payload",
			role: RolePlan,
			resp: &contracts.AgentResponse{
				Status: "ok",
				Plan:   &plan.PlanOutput{},
			},
			wantErr: false,
		},
		{
			name: "plan ok missing payload",
			role: RolePlan,
			resp: &contracts.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "plan stop without payload",
			role: RolePlan,
			resp: &contracts.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "plan error status",
			role: RolePlan,
			resp: &contracts.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "do ok with payload",
			role: RoleDo,
			resp: &contracts.AgentResponse{
				Status: "ok",
				Do:     &do.DoOutput{},
			},
			wantErr: false,
		},
		{
			name: "do ok missing payload",
			role: RoleDo,
			resp: &contracts.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "do stop without payload",
			role: RoleDo,
			resp: &contracts.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "do error status",
			role: RoleDo,
			resp: &contracts.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "check ok with payload",
			role: RoleCheck,
			resp: &contracts.AgentResponse{
				Status: "ok",
				Check:  &check.CheckOutput{},
			},
			wantErr: false,
		},
		{
			name: "check ok missing payload",
			role: RoleCheck,
			resp: &contracts.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "check error status",
			role: RoleCheck,
			resp: &contracts.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "act ok with payload",
			role: RoleAct,
			resp: &contracts.AgentResponse{
				Status: "ok",
				Act:    &act.ActOutput{},
			},
			wantErr: false,
		},
		{
			name: "act ok missing payload",
			role: RoleAct,
			resp: &contracts.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "unknown role",
			role: "unknown",
			resp: &contracts.AgentResponse{
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

func TestNewLoopAgentRegistersRoleSubAgents(t *testing.T) {
	t.Parallel()

	loopAgent, err := NewLoopAgent(
		config.Config{},
		nil,
		AgentInput{},
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
	wantNames := []string{RolePlan, RoleDo, RoleCheck, RoleAct}
	for _, want := range wantNames {
		if !slices.Contains(gotNames, want) {
			t.Fatalf("missing sub-agent %q, got %v", want, gotNames)
		}
	}
}

func TestCommitWorkspaceChangesCommitsDirtyWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initTestRepo(t, ctx, repoRoot)

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\n")
	runGit(t, ctx, repoRoot, "add", "a.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\ntwo\n")
	writeTestFile(t, filepath.Join(repoRoot, "b.txt"), "new\n")

	if err := commitWorkspaceChanges(ctx, repoRoot, "run-1", "norma-8sl", 2); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))
	if after == before {
		t.Fatalf("expected a new commit, HEAD unchanged at %s", after)
	}

	commitMsg := runGit(t, ctx, repoRoot, "log", "-1", "--pretty=%B")
	if !strings.Contains(commitMsg, "chore: do step 002") {
		t.Fatalf("commit message missing step info:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Run: run-1") {
		t.Fatalf("commit message missing run id:\n%s", commitMsg)
	}
	if !strings.Contains(commitMsg, "Task: norma-8sl") {
		t.Fatalf("commit message missing task id:\n%s", commitMsg)
	}

	status := strings.TrimSpace(runGit(t, ctx, repoRoot, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected clean workspace after commit, got:\n%s", status)
	}
}

func TestCommitWorkspaceChangesNoopForCleanWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	initTestRepo(t, ctx, repoRoot)

	writeTestFile(t, filepath.Join(repoRoot, "a.txt"), "one\n")
	runGit(t, ctx, repoRoot, "add", "a.txt")
	runGit(t, ctx, repoRoot, "commit", "-m", "chore: initial")
	before := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))

	if err := commitWorkspaceChanges(ctx, repoRoot, "run-2", "norma-8sl", 3); err != nil {
		t.Fatalf("commitWorkspaceChanges() error = %v", err)
	}

	after := strings.TrimSpace(runGit(t, ctx, repoRoot, "rev-parse", "HEAD"))
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

func initTestRepo(t *testing.T, ctx context.Context, repoRoot string) {
	t.Helper()
	runGit(t, ctx, repoRoot, "init")
	runGit(t, ctx, repoRoot, "config", "user.name", "Norma Test")
	runGit(t, ctx, repoRoot, "config", "user.email", "norma-test@example.com")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func runGit(t *testing.T, ctx context.Context, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
