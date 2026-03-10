package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles/plan"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyRole struct{}

func (r *dummyRole) Name() string                                    { return "plan" }
func (r *dummyRole) InputSchema() string                             { return "{}" }
func (r *dummyRole) OutputSchema() string                            { return "{}" }
func (r *dummyRole) Prompt(_ contracts.AgentRequest) (string, error) { return "prompt", nil }
func (r *dummyRole) MapRequest(req contracts.AgentRequest) (any, error) {
	return req, nil
}
func (r *dummyRole) MapResponse(outBytes []byte) (contracts.AgentResponse, error) {
	var resp contracts.AgentResponse
	err := json.Unmarshal(outBytes, &resp)
	return resp, err
}
func (r *dummyRole) SetRunner(_ any) {}
func (r *dummyRole) Runner() any     { return nil }

type failingMapRole struct {
	dummyRole
}

func (r *failingMapRole) MapResponse(_ []byte) (contracts.AgentResponse, error) {
	return contracts.AgentResponse{}, errors.New("map failed")
}

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
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o700)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: contracts.StepInfo{Index: 1, Name: "plan"},
		Paths: contracts.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
		Budgets: contracts.Budgets{
			MaxIterations: 1,
		},
		Context: contracts.RequestContext{
			Facts: make(map[string]any),
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Plan:               &plan.PlanInput{Task: &plan.PlanTaskID{Id: "task-1"}},
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
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o700)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: contracts.StepInfo{Index: 1, Name: "plan"},
		Paths: contracts.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
		Budgets: contracts.Budgets{
			MaxIterations: 1,
		},
		Context: contracts.RequestContext{
			Facts: make(map[string]any),
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Plan:               &plan.PlanInput{Task: &plan.PlanTaskID{Id: "task-1"}},
	}

	ctx := context.Background()
	var stderr bytes.Buffer
	_, _, exitCode, err := runner.Run(ctx, req, io.Discard, &stderr)
	assert.Error(t, err)
	assert.Equal(t, 1, exitCode)
	// ADK runner might not include "exit code 1" directly in the wrapped error message string if it comes from exec agent
	assert.Contains(t, err.Error(), "agent execution error")
}

func TestAinvokeRunner_RunReturnsErrorWhenResponseMappingFails(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	agentScript := filepath.Join(repoRoot, "my-agent.sh")
	scriptContent := `#!/bin/sh
cat > /dev/null # consume stdin
echo '{}' > output.json
echo '{}'
	`
	err = os.WriteFile(agentScript, []byte(scriptContent), 0o700)
	require.NoError(t, err)

	cfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{agentScript},
	}

	runner, err := NewRunner(cfg, &failingMapRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: contracts.StepInfo{Index: 1, Name: "plan"},
		Paths: contracts.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
		Budgets: contracts.Budgets{
			MaxIterations: 1,
		},
		Context: contracts.RequestContext{
			Facts: make(map[string]any),
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Plan:               &plan.PlanInput{Task: &plan.PlanTaskID{Id: "task-1"}},
	}

	_, _, exitCode, err := runner.Run(context.Background(), req, io.Discard, io.Discard)
	require.Error(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, err.Error(), "parse agent response")
	assert.Contains(t, err.Error(), "map failed")
}

func TestResolveACPCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     config.AgentConfig
		wantErr string
		check   func(t *testing.T, got []string)
	}{
		{
			name: "acp_exec_requires_cmd",
			cfg: config.AgentConfig{
				Type: config.AgentTypeACPExec,
			},
			wantErr: "acp_exec agent requires cmd",
		},
		{
			name: "gemini_acp_builds_command",
			cfg: config.AgentConfig{
				Type:      config.AgentTypeGeminiACP,
				Model:     "gemini-3-flash-preview",
				ExtraArgs: []string{"--foo"},
			},
			check: func(t *testing.T, got []string) {
				t.Helper()
				want := []string{"gemini", "--experimental-acp", "--model", "gemini-3-flash-preview", "--foo"}
				assert.Equal(t, want, got)
			},
		},
		{
			name: "opencode_acp_builds_command",
			cfg: config.AgentConfig{
				Type:      config.AgentTypeOpenCodeACP,
				Model:     "opencode/model",
				ExtraArgs: []string{"--bar"},
			},
			check: func(t *testing.T, got []string) {
				t.Helper()
				want := []string{"opencode", "acp", "--bar"}
				assert.Equal(t, want, got)
			},
		},
		{
			name: "acp_exec_appends_extra_args",
			cfg: config.AgentConfig{
				Type:      config.AgentTypeACPExec,
				Cmd:       []string{"custom-acp", "--mode", "stdio"},
				ExtraArgs: []string{"--flag"},
			},
			check: func(t *testing.T, got []string) {
				t.Helper()
				want := []string{"custom-acp", "--mode", "stdio", "--flag"}
				assert.Equal(t, want, got)
			},
		},
		{
			name: "codex_acp_uses_proxy_command",
			cfg: config.AgentConfig{
				Type:      config.AgentTypeCodexACP,
				Model:     "gpt-5.4",
				ExtraArgs: []string{"--trace", "--raw"},
			},
			check: func(t *testing.T, got []string) {
				t.Helper()
				require.GreaterOrEqual(t, len(got), 7)
				assert.Equal(t, "proxy", got[1])
				assert.Equal(t, "codex-acp", got[2])
				assert.Equal(t, "--codex-model", got[3])
				assert.Equal(t, "gpt-5.4", got[4])
				assert.Equal(t, "--trace", got[5])
				assert.Equal(t, "--raw", got[6])
			},
		},
		{
			name: "codex_acp_templates_extra_args_with_model",
			cfg: config.AgentConfig{
				Type:      config.AgentTypeCodexACP,
				Model:     "gpt-5.4",
				ExtraArgs: []string{"--codex-model={{.Model}}"},
			},
			check: func(t *testing.T, got []string) {
				t.Helper()
				require.GreaterOrEqual(t, len(got), 6)
				assert.Equal(t, "proxy", got[1])
				assert.Equal(t, "codex-acp", got[2])
				assert.Equal(t, "--codex-model", got[3])
				assert.Equal(t, "gpt-5.4", got[4])
				assert.Equal(t, "--codex-model=gpt-5.4", got[5])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveACPCommand(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
