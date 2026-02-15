package normaloop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metalagman/norma/internal/workflows"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
)

func TestApplyAgentResponseToTaskStateActPersistsOutputAndJournal(t *testing.T) {
	t.Parallel()

	state := &models.TaskState{}
	resp := &models.AgentResponse{
		Status:     "ok",
		StopReason: "none",
		Progress: models.StepProgress{
			Title:   "Act decision applied",
			Details: []string{"Decision close"},
		},
		Act: &models.ActOutput{
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

	state := &models.TaskState{}
	resp := &models.AgentResponse{
		Status:     "ok",
		StopReason: "none",
		Progress: models.StepProgress{
			Details: []string{"no explicit title"},
		},
		Act: &models.ActOutput{
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

	agent := &NormaLoopAgent{
		runInput: workflows.RunInput{
			TaskID: "norma-95b",
			RunID:  "run-default",
		},
	}

	stepDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stepDir, "artifacts"), 0o755); err != nil {
		t.Fatalf("create artifacts dir: %v", err)
	}

	state := &models.TaskState{
		Journal: []models.JournalEntry{
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

	original := &models.TaskState{
		Act: &models.ActOutput{Decision: "close"},
	}
	gotPtr := coerceTaskState(original)
	if gotPtr != original {
		t.Fatalf("coerceTaskState(pointer) should return same pointer")
	}

	value := models.TaskState{
		Act: &models.ActOutput{Decision: "replan"},
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
