package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
	"database/sql"
)

type fakeAgent struct {
	responses map[string]model.AgentResponse
	requests  []model.AgentRequest
}

func (a *fakeAgent) Run(ctx context.Context, req model.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	a.requests = append(a.requests, req)
	resp, ok := a.responses[req.Step.Name]
	if !ok {
		return nil, nil, 1, fmt.Errorf("no response for role %s", req.Step.Name)
	}

	if req.Step.Name == "do" && req.Paths.WorkspaceDir != "" {
		// Simulate work in workspace
		testFile := filepath.Join(req.Paths.WorkspaceDir, "test.txt")
		_ = os.WriteFile(testFile, []byte("some changes"), 0o644)
		// We must commit in workspace as per orchestrator expectations (though Runner.Run doesn't strictly check workspace commits for Do, it helps tests)
		cmd := exec.CommandContext(ctx, "git", "add", "test.txt")
		cmd.Dir = req.Paths.WorkspaceDir
		_ = cmd.Run()
		cmd = exec.CommandContext(ctx, "git", "commit", "-m", "do: work")
		cmd.Dir = req.Paths.WorkspaceDir
		_ = cmd.Run()
	}

	data, _ := json.Marshal(resp)
	stdout.Write(data)
	return data, nil, 0, nil
}

func (a *fakeAgent) Describe() agent.RunnerInfo {
	return agent.RunnerInfo{Type: "fake"}
}

type fakeTracker struct {
	task.Tracker
	statuses map[string]string
	tasks    map[string]task.Task
}

func (t *fakeTracker) MarkStatus(ctx context.Context, id, status string) error {
	if t.statuses == nil {
		t.statuses = make(map[string]string)
	}
	t.statuses[id] = status
	return nil
}

func (t *fakeTracker) Get(ctx context.Context, id string) (task.Task, error) {
	if tk, ok := t.tasks[id]; ok {
		return tk, nil
	}
	return task.Task{ID: id}, nil
}

func (t *fakeTracker) SetNotes(ctx context.Context, id string, notes string) error {
	if tk, ok := t.tasks[id]; ok {
		tk.Notes = notes
		t.tasks[id] = tk
	}
	return nil
}

func (t *fakeTracker) AddLabel(ctx context.Context, id string, label string) error {
	if tk, ok := t.tasks[id]; ok {
		tk.Labels = append(tk.Labels, label)
		t.tasks[id] = tk
	}
	return nil
}

func (t *fakeTracker) RemoveLabel(ctx context.Context, id string, label string) error {
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

	runCmdErr(context.Background(), dir, "git", "init")
	runCmdErr(context.Background(), dir, "git", "config", "user.email", "test@example.com")
	runCmdErr(context.Background(), dir, "git", "config", "user.name", "test")
	runCmdErr(context.Background(), dir, "git", "commit", "--allow-empty", "-m", "initial commit")

	return dir
}

func TestRunner_Run_Success(t *testing.T) {
	repoRoot := setupTestRepo(t)
	defer os.RemoveAll(repoRoot)

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

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
		responses: map[string]model.AgentResponse{
			"plan": {
				Status: "ok",
				Summary: model.ResponseSummary{Text: "Planned"},
				Progress: model.StepProgress{Title: "Planning done"},
				Plan: &model.PlanOutput{
					WorkPlan: model.WorkPlan{
						DoSteps: []model.DoStep{{ID: "DO-1"}},
					},
					AcceptanceCriteria: model.EffectiveCriteriaGroup{
						Effective: []model.EffectiveAcceptanceCriterion{
							{ID: "AC-1", Text: "Effectively checked"},
						},
					},
				},
			},
			"do": {
				Status: "ok",
				Summary: model.ResponseSummary{Text: "Did it"},
				Progress: model.StepProgress{Title: "Doing done"},
				Do: &model.DoOutput{
					Execution: model.DoExecution{ExecutedStepIDs: []string{"DO-1"}},
				},
			},
			"check": {
				Status: "ok",
				Summary: model.ResponseSummary{Text: "Checked"},
				Progress: model.StepProgress{Title: "Checking done"},
				Check: &model.CheckOutput{
					Verdict: model.CheckVerdict{Status: "PASS"},
				},
			},
			"act": {
				Status: "ok",
				Summary: model.ResponseSummary{Text: "Acted"},
				Progress: model.StepProgress{Title: "Acting done"},
				Act: &model.ActOutput{Decision: "close"},
			},
		},
	}

	runner := &Runner{
		repoRoot: repoRoot,
		normaDir: filepath.Join(repoRoot, ".norma"),
		cfg: config.Config{
			Budgets: config.Budgets{MaxIterations: 1},
		},
		store:   store,
		agents:  map[string]agent.Runner{
			"plan":  fAgent,
			"do":    fAgent,
			"check": fAgent,
			"act":   fAgent,
		},
		tracker: tracker,
	}

	ctx := context.Background()
	res, err := runner.Run(ctx, "Test goal", nil, "norma-123")
	require.NoError(t, err)
	assert.Equal(t, "passed", res.Status)

	// Verify progress.md
	progressPath := filepath.Join(runner.artifactsDir, "progress.md")
	_, err = os.Stat(progressPath)
	assert.NoError(t, err)

	// Verify sequence
	assert.Equal(t, 4, len(fAgent.requests))
	assert.Equal(t, "plan", fAgent.requests[0].Step.Name)
	assert.Equal(t, "do", fAgent.requests[1].Step.Name)
	assert.Equal(t, "check", fAgent.requests[2].Step.Name)
	assert.Equal(t, "act", fAgent.requests[3].Step.Name)

			// Verify tracker status

			assert.Equal(t, "acting", tracker.statuses["norma-123"])

		}

		

		func TestRunner_Run_ReusePlan(t *testing.T) {

			repoRoot := setupTestRepo(t)

			defer os.RemoveAll(repoRoot)

		

			db, err := sql.Open("sqlite", ":memory:")

			require.NoError(t, err)

			defer db.Close()

		

			// Initialize schema (reused from above, but for brevity in this specific test)

			_, err = db.Exec(`

				CREATE TABLE runs (run_id TEXT PRIMARY KEY, created_at TEXT NOT NULL, goal TEXT NOT NULL, status TEXT NOT NULL, iteration INTEGER NOT NULL, current_step_index INTEGER NOT NULL, verdict TEXT, run_dir TEXT NOT NULL);

				CREATE TABLE steps (run_id TEXT NOT NULL, step_index INTEGER NOT NULL, role TEXT NOT NULL, iteration INTEGER NOT NULL, status TEXT NOT NULL, step_dir TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT NOT NULL, summary TEXT, PRIMARY KEY (run_id, step_index));

				CREATE TABLE events (run_id TEXT NOT NULL, seq INTEGER NOT NULL, ts TEXT NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, data_json TEXT, PRIMARY KEY (run_id, seq));

			`)

			require.NoError(t, err)

		

				store := NewStore(db)

		

				

		

				stateJSON, _ := json.Marshal(model.TaskState{

		

					Plan: &model.PlanOutput{

		

						WorkPlan: model.WorkPlan{

		

							DoSteps: []model.DoStep{{ID: "DO-EXISTING"}},

		

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

				responses: map[string]model.AgentResponse{

					"do": {

						Status: "ok",

						Summary: model.ResponseSummary{Text: "Did it"},

						Progress: model.StepProgress{Title: "Doing done"},

						Do: &model.DoOutput{

							Execution: model.DoExecution{ExecutedStepIDs: []string{"DO-EXISTING"}},

						},

					},

					"check": {

						Status: "ok",

						Summary: model.ResponseSummary{Text: "Checked"},

						Progress: model.StepProgress{Title: "Checking done"},

						Check: &model.CheckOutput{

							Verdict: model.CheckVerdict{Status: "PASS"},

						},

					},

					"act": {

						Status: "ok",

						Summary: model.ResponseSummary{Text: "Acted"},

						Progress: model.StepProgress{Title: "Acting done"},

						Act: &model.ActOutput{Decision: "close"},

					},

				},

			}

		

			runner := &Runner{

				repoRoot: repoRoot,

				normaDir: filepath.Join(repoRoot, ".norma"),

				cfg: config.Config{

					Budgets: config.Budgets{MaxIterations: 1},

				},

				store:   store,

				agents:  map[string]agent.Runner{

					"do":    fAgent,

					"check": fAgent,

					"act":   fAgent,

				},

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

		

	