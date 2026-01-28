// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"database/sql"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/metalagman/norma/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

type fakeAgent struct {
	responses map[string]normaloop.AgentResponse
	requests  []normaloop.AgentRequest
}

func (a *fakeAgent) Run(ctx context.Context, req normaloop.AgentRequest, stdout, _ io.Writer) ([]byte, []byte, int, error) {
	a.requests = append(a.requests, req)
	resp, ok := a.responses[req.Step.Name]
	if !ok {
		return nil, nil, 1, fmt.Errorf("no response for role %s", req.Step.Name)
	}

	if req.Step.Name == "do" && req.Paths.WorkspaceDir != "" && req.Paths.RunDir != "" {
		// Simulate work in workspace
		testFile := filepath.Join(req.Paths.WorkspaceDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("some changes"), 0o644); err != nil {
			return nil, nil, 1, fmt.Errorf("write test file: %w", err)
		}
	}

	data, _ := json.Marshal(resp)
	_, _ = stdout.Write(data)
	return data, nil, 0, nil
}

type fakeTracker struct {
	task.Tracker
	statuses map[string]string
	tasks    map[string]task.Task
}

func (t *fakeTracker) MarkStatus(_ context.Context, id, status string) error {
	if t.statuses == nil {
		t.statuses = make(map[string]string)
	}
	t.statuses[id] = status
	return nil
}

func (t *fakeTracker) Task(_ context.Context, id string) (task.Task, error) {
	if tk, ok := t.tasks[id]; ok {
		return tk, nil
	}
	return task.Task{ID: id}, nil
}

func (t *fakeTracker) SetNotes(_ context.Context, id string, notes string) error {
	if tk, ok := t.tasks[id]; ok {
		tk.Notes = notes
		t.tasks[id] = tk
	}
	return nil
}

func (t *fakeTracker) AddLabel(_ context.Context, id string, label string) error {
	if tk, ok := t.tasks[id]; ok {
		tk.Labels = append(tk.Labels, label)
		t.tasks[id] = tk
	}
	return nil
}

func (t *fakeTracker) RemoveLabel(_ context.Context, id string, label string) error {
	if tk, ok := t.tasks[id]; ok {
		var newLabels []string
		for _, l := range tk.Labels {
			if l != label {
				newLabels = append(newLabels, l)
			}
		}
		tk.Labels = newLabels
		t.tasks[id] = tk
	}
	return nil
}

func setupTestRepo(t *testing.T) string {
	dir, err := os.MkdirTemp("", "norma-test-*")
	require.NoError(t, err)

	ctx := context.Background()
	_ = runCmdErr(ctx, dir, "git", "init")
	_ = runCmdErr(ctx, dir, "git", "config", "user.email", "test@example.com")
	_ = runCmdErr(ctx, dir, "git", "config", "user.name", "test")
	_ = runCmdErr(ctx, dir, "git", "commit", "--allow-empty", "-m", "initial commit")

	return dir
}

func TestRunner_Run_Success(t *testing.T) {
	repoRoot := setupTestRepo(t)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Initialize schema
	_, err = db.Exec(`
		CREATE TABLE runs (
			run_id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			goal TEXT NOT NULL,
			status TEXT NOT NULL,
			iteration INTEGER NOT NULL,
			current_step_index INTEGER NOT NULL,
			verdict TEXT,
			run_dir TEXT NOT NULL
		);
		CREATE TABLE steps (
			run_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			role TEXT NOT NULL,
			iteration INTEGER NOT NULL,
			status TEXT NOT NULL,
			step_dir TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL,
			summary TEXT,
			PRIMARY KEY (run_id, step_index)
		);
		CREATE TABLE events (
			run_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			ts TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL,
			data_json TEXT,
			PRIMARY KEY (run_id, seq)
		);
	`)
	require.NoError(t, err)

	store := NewStore(db)
	tracker := &fakeTracker{
		tasks: make(map[string]task.Task),
	}

	fAgent := &fakeAgent{
		responses: map[string]normaloop.AgentResponse{
			"plan": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Planned"},
				Progress: normaloop.StepProgress{Title: "Planning done"},
				Plan: &normaloop.PlanOutput{
					WorkPlan: normaloop.WorkPlan{
						DoSteps: []normaloop.DoStep{{ID: "DO-1"}},
					},
					AcceptanceCriteria: normaloop.EffectiveCriteriaGroup{
						Effective: []normaloop.EffectiveAcceptanceCriterion{
							{ID: "AC-1", Text: "Effectively checked"},
						},
					},
				},
			},
			"do": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Did it"},
				Progress: normaloop.StepProgress{Title: "Doing done"},
				Do: &normaloop.DoOutput{
					Execution: normaloop.DoExecution{ExecutedStepIDs: []string{"DO-1"}},
				},
			},
			"check": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Checked"},
				Progress: normaloop.StepProgress{Title: "Checking done"},
				Check: &normaloop.CheckOutput{
					Verdict: normaloop.CheckVerdict{Status: "PASS"},
				},
			},
			"act": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Acted"},
				Progress: normaloop.StepProgress{Title: "Acting done"},
				Act:      &normaloop.ActOutput{Decision: "close"},
			},
		},
	}

	normaloop.GetRole("plan").SetRunner(fAgent)
	normaloop.GetRole("do").SetRunner(fAgent)
	normaloop.GetRole("check").SetRunner(fAgent)
	normaloop.GetRole("act").SetRunner(fAgent)

	runner := &Runner{
		repoRoot: repoRoot,
		normaDir: filepath.Join(repoRoot, ".norma"),
		cfg: config.Config{
			Budgets: config.Budgets{MaxIterations: 1},
		},
		store:   store,
		tracker: tracker,
	}

	ctx := context.Background()
	res, err := runner.Run(ctx, "Test goal", nil, "norma-123")
	require.NoError(t, err)
	assert.Equal(t, "passed", res.Status)

	// Verify progress.md in the last step's artifacts directory
	lastStepDir := filepath.Join(runner.runDir, "steps", "04-act", "artifacts", "progress.md")
	_, err = os.Stat(lastStepDir)
	assert.NoError(t, err)

	// Verify sequence
	assert.Equal(t, 4, len(fAgent.requests))
	assert.Equal(t, "plan", fAgent.requests[0].Step.Name)
	assert.Equal(t, "do", fAgent.requests[1].Step.Name)
	assert.Equal(t, "check", fAgent.requests[2].Step.Name)
	assert.Equal(t, "act", fAgent.requests[3].Step.Name)

	// Verify tracker status
	assert.Equal(t, "done", tracker.statuses["norma-123"])
}

func TestRunner_Run_ReusePlan(t *testing.T) {
	repoRoot := setupTestRepo(t)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Initialize schema
	_, err = db.Exec(`
		CREATE TABLE runs (run_id TEXT PRIMARY KEY, created_at TEXT NOT NULL, goal TEXT NOT NULL, status TEXT NOT NULL, iteration INTEGER NOT NULL, current_step_index INTEGER NOT NULL, verdict TEXT, run_dir TEXT NOT NULL);
		CREATE TABLE steps (run_id TEXT NOT NULL, step_index INTEGER NOT NULL, role TEXT NOT NULL, iteration INTEGER NOT NULL, status TEXT NOT NULL, step_dir TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT NOT NULL, summary TEXT, PRIMARY KEY (run_id, step_index));
		CREATE TABLE events (run_id TEXT NOT NULL, seq INTEGER NOT NULL, ts TEXT NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, data_json TEXT, PRIMARY KEY (run_id, seq));
	`)
	require.NoError(t, err)

	store := NewStore(db)

	stateJSON, _ := json.Marshal(normaloop.TaskState{
		Plan: &normaloop.PlanOutput{
			WorkPlan: normaloop.WorkPlan{
				DoSteps: []normaloop.DoStep{{ID: "DO-EXISTING"}},
			},
		},
	})

	tracker := &fakeTracker{
		tasks: map[string]task.Task{
			"norma-preplanned": {
				ID:     "norma-preplanned",
				Labels: []string{"norma-has-plan"},
				Notes:  string(stateJSON),
			},
		},
	}

	fAgent := &fakeAgent{
		responses: map[string]normaloop.AgentResponse{
			"do": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Did it"},
				Progress: normaloop.StepProgress{Title: "Doing done"},
				Do: &normaloop.DoOutput{
					Execution: normaloop.DoExecution{ExecutedStepIDs: []string{"DO-EXISTING"}},
				},
			},
			"check": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Checked"},
				Progress: normaloop.StepProgress{Title: "Checking done"},
				Check: &normaloop.CheckOutput{
					Verdict: normaloop.CheckVerdict{Status: "PASS"},
				},
			},
			"act": {
				Status:   "ok",
				Summary:  normaloop.ResponseSummary{Text: "Acted"},
				Progress: normaloop.StepProgress{Title: "Acting done"},
				Act:      &normaloop.ActOutput{Decision: "close"},
			},
		},
	}

	normaloop.GetRole("do").SetRunner(fAgent)
	normaloop.GetRole("check").SetRunner(fAgent)
	normaloop.GetRole("act").SetRunner(fAgent)

	runner := &Runner{
		repoRoot: repoRoot,
		normaDir: filepath.Join(repoRoot, ".norma"),
		cfg: config.Config{
			Budgets: config.Budgets{MaxIterations: 1},
		},
		store:   store,
		tracker: tracker,
	}

	ctx := context.Background()
	res, err := runner.Run(ctx, "Test goal", nil, "norma-preplanned")
	require.NoError(t, err)
	assert.Equal(t, "passed", res.Status)

	// Verify sequence: plan should NOT be in requests
	for _, req := range fAgent.requests {
		assert.NotEqual(t, "plan", req.Step.Name)
	}
	assert.Equal(t, "do", fAgent.requests[0].Step.Name)
}
