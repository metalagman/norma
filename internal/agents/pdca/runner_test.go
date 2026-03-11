package pdca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	acp "github.com/coder/acp-go-sdk"
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
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		Cmd:  []string{"custom-acp", "--stdio"},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	assert.NoError(t, err)
	assert.NotNil(t, runner)
}

func TestAinvokeRunner_Run(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		Cmd:  helperACPCommand(t, `{"status":"ok","summary":{"text":"success"},"progress":{"title":"done","details":[]}}`),
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

	var resp contracts.AgentResponse
	err = json.Unmarshal(stdout, &resp)
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
}

func TestAinvokeRunner_RunWritesErrorToStderr(t *testing.T) {
	// For ACP agents, errors are usually reported via the protocol or connection failure.
	// Here we simulate a connection failure (binary not found).
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		Cmd:  []string{"/non/existent/binary"},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: contracts.StepInfo{Index: 1, Name: "plan"},
		Paths: contracts.RequestPaths{
			WorkspaceDir: t.TempDir(),
			RunDir:       t.TempDir(),
		},
		Budgets:            contracts.Budgets{MaxIterations: 1},
		StopReasonsAllowed: []string{"budget_exceeded"},
	}

	ctx := context.Background()
	var stderr bytes.Buffer
	_, _, exitCode, err := runner.Run(ctx, req, io.Discard, &stderr)
	assert.Error(t, err)
	assert.NotEqual(t, 0, exitCode)
}

func TestAinvokeRunner_RunReturnsErrorWhenResponseMappingFails(t *testing.T) {
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		Cmd:  helperACPCommand(t, "{}"),
	}

	runner, err := NewRunner(cfg, &failingMapRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "text"}}},
		Step: contracts.StepInfo{Index: 1, Name: "plan"},
		Paths: contracts.RequestPaths{
			WorkspaceDir: t.TempDir(),
			RunDir:       t.TempDir(),
		},
		Budgets:            contracts.Budgets{MaxIterations: 1},
		StopReasonsAllowed: []string{"budget_exceeded"},
	}

	_, _, exitCode, err := runner.Run(context.Background(), req, io.Discard, io.Discard)
	require.Error(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, err.Error(), "map agent response")
	assert.Contains(t, err.Error(), "map failed")
}

func helperACPCommand(t *testing.T, response string) []string {
	t.Helper()
	return []string{
		"env",
		"GO_WANT_AGENT_ACP_HELPER=1",
		"GO_HELPER_RESPONSE=" + response,
		os.Args[0],
		"-test.run=TestAgentACPHelperProcess",
		"--",
	}
}

func TestAgentACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENT_ACP_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}

		switch req.Method {
		case acp.AgentMethodInitialize:
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": acp.ProtocolVersionNumber,
				},
			})
		case acp.AgentMethodSessionNew:
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessionId": "session-1",
				},
			})
		case acp.AgentMethodSessionPrompt:
			// Send response
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  acp.ClientMethodSessionUpdate,
				"params": map[string]any{
					"sessionId": "session-1",
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content": map[string]any{
							"type": "text",
							"text": os.Getenv("GO_HELPER_RESPONSE"),
						},
					},
				},
			})
			// Finalize prompt
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"stopReason": "end_turn",
				},
			})
		}
	}
	os.Exit(0)
}
