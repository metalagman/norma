package adkpdca

import (
	"testing"
	"time"

	"github.com/metalagman/norma/internal/workflows/normaloop"
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
			Decision:  "close",
			Rationale: "all checks passed",
			Next: models.NextAction{
				Recommended: true,
				Notes:       "no follow-up needed",
			},
		},
	}

	ts := time.Date(2026, time.February, 12, 13, 14, 15, 0, time.UTC)
	applyAgentResponseToTaskState(state, resp, normaloop.RoleAct, 4, ts)

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
	if entry.Role != normaloop.RoleAct {
		t.Fatalf("journal role = %q, want %q", entry.Role, normaloop.RoleAct)
	}
	if entry.StepIndex != 4 {
		t.Fatalf("journal step index = %d, want 4", entry.StepIndex)
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
			Decision:  "replan",
			Rationale: "partial verification",
		},
	}

	ts := time.Date(2026, time.February, 12, 13, 14, 15, 0, time.UTC)
	applyAgentResponseToTaskState(state, resp, normaloop.RoleAct, 5, ts)

	if len(state.Journal) != 1 {
		t.Fatalf("len(state.Journal) = %d, want 1", len(state.Journal))
	}
	if state.Journal[0].Title != "act step completed" {
		t.Fatalf("journal title = %q, want %q", state.Journal[0].Title, "act step completed")
	}
}
