package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunner(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{"echo", "test"},
	}

	runner, err := NewRunner(cfg, repoRoot)
	assert.NoError(t, err)
	assert.NotNil(t, runner)

	info := runner.Describe()
	assert.Equal(t, "exec", info.Type)
	assert.Equal(t, []string{"echo", "test"}, info.Cmd)
}

func TestAinvokeRunner_Run(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	// Create a dummy agent that just writes a valid AgentResponse to output.json
	agentScript := filepath.Join(repoRoot, "my-agent.sh")
	scriptContent := `#!/bin/sh
cat > /dev/null # consume stdin
RESP='{"status":"ok","summary":{"text":"success"},"progress":{"title":"done","details":[]},"plan":{"task_id":"task-1","goal":"goal","acceptance_criteria":{"baseline":[],"effective":[]},"work_plan":{"timebox_minutes":10,"do_steps":[],"check_steps":[],"stop_triggers":[]}}}'
echo "$RESP" > output.json
echo "$RESP"
`
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o755)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, repoRoot)
	require.NoError(t, err)

	req := model.AgentRequest{
		Run:  model.RunInfo{ID: "run-1", Iteration: 1},
		Task: model.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []model.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: model.StepInfo{Index: 1, Name: "plan", Dir: repoRoot},
		Paths: model.RequestPaths{
			WorkspaceDir: repoRoot,
			WorkspaceMode: "read_only",
			RunDir: repoRoot,
			CodeRoot: repoRoot,
		},
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
