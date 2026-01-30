package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyRole struct{}

func (r *dummyRole) Name() string                                { return "plan" }
func (r *dummyRole) InputSchema() string                         { return "{}" }
func (r *dummyRole) OutputSchema() string                        { return "{}" }
func (r *dummyRole) Prompt(_ models.AgentRequest) (string, error) { return "prompt", nil }
func (r *dummyRole) MapRequest(req models.AgentRequest) (any, error) {
	return req, nil
}
func (r *dummyRole) MapResponse(outBytes []byte) (models.AgentResponse, error) {
	var resp models.AgentResponse
	err := json.Unmarshal(outBytes, &resp)
	return resp, err
}
func (r *dummyRole) SetRunner(_ any) {}
func (r *dummyRole) Runner() any     { return nil }

func TestNewRunner(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{"echo", "test"},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	assert.NoError(t, err)
	assert.NotNil(t, runner)
}

func TestAinvokeRunner_Run(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	// Create a dummy agent that just writes a valid AgentResponse to output.json
	agentScript := filepath.Join(repoRoot, "my-agent.sh")
	scriptContent := `#!/bin/sh
cat > /dev/null # consume stdin
RESP='{"status":"ok","summary":{"text":"success"},"progress":{"title":"done","details":[]},"plan_output":{"task_id":"task-1","goal":"goal","acceptance_criteria":{"baseline":[],"effective":[]},"work_plan":{"timebox_minutes":10,"do_steps":[],"check_steps":[],"stop_triggers":[]}}}'
echo "$RESP" > output.json
echo "$RESP"
`
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o755)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := models.AgentRequest{
		Run:  models.RunInfo{ID: "run-1", Iteration: 1},
		Task: models.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: models.StepInfo{Index: 1, Name: "plan"},
		Paths: models.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
		Budgets: models.Budgets{
			MaxIterations: 1,
		},
		Context: models.RequestContext{
			Facts: make(map[string]any),
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Plan:               &models.PlanInput{Task: models.IDInfo{ID: "task-1"}},
	}

	ctx := context.Background()
	stdout, stderr, exitCode, err := runner.Run(ctx, req, io.Discard, io.Discard)
	assert.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)
	assert.NotEmpty(t, stdout)

	// Check if input.json was created
	_, err = os.Stat(filepath.Join(repoRoot, "input.json"))
	assert.NoError(t, err)

	// Check if output.json was created (by the agent)
	_, err = os.Stat(filepath.Join(repoRoot, "output.json"))
	assert.NoError(t, err)
}

func TestAinvokeRunner_RunWritesErrorToStderr(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	agentScript := filepath.Join(repoRoot, "my-agent.sh")
	scriptContent := `#!/bin/sh
echo "boom" 1>&2
exit 1
`
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o755)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := models.AgentRequest{
		Run:  models.RunInfo{ID: "run-1", Iteration: 1},
		Task: models.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: models.StepInfo{Index: 1, Name: "plan"},
		Paths: models.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
		Budgets: models.Budgets{
			MaxIterations: 1,
		},
		Context: models.RequestContext{
			Facts: make(map[string]any),
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Plan:               &models.PlanInput{Task: models.IDInfo{ID: "task-1"}},
	}

	ctx := context.Background()
	var stderr bytes.Buffer
	_, _, exitCode, err := runner.Run(ctx, req, io.Discard, &stderr)
	assert.Error(t, err)
	assert.Equal(t, 1, exitCode)
	assert.Contains(t, stderr.String(), "exit code 1")
}
