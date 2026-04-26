package pdca

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/internal/agents/pdca/contracts"
	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/structuredagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyRole struct{}

func (r *dummyRole) Name() string { return "plan" }
func (r *dummyRole) Schemas() contracts.SchemaPair {
	return contracts.SchemaPair{InputSchema: "{}", OutputSchema: "{}"}
}
func (r *dummyRole) Prompt(_ contracts.RawAgentRequest) (string, error) { return "prompt", nil }
func (r *dummyRole) MapRequest(req contracts.RawAgentRequest) (any, error) {
	return req, nil
}
func (r *dummyRole) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var resp contracts.RawAgentResponse
	err := json.Unmarshal(outBytes, &resp)
	return resp, err
}

type cwdPromptRole struct {
	dummyRole
}

func (r *cwdPromptRole) Prompt(_ contracts.RawAgentRequest) (string, error) {
	return "cwd={cwd}", nil
}

func runnerTestRequest(workingDir string) []byte {
	return []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal","acceptance_criteria":[{"id":"AC1","text":"text","verify_hints":[]}]},"step":{"index":1},"paths":{"workspace_dir":"` + workingDir + `"}}`)
}

type failingMapRole struct {
	dummyRole
}

func (r *failingMapRole) MapResponse(_ []byte) (contracts.RawAgentResponse, error) {
	return contracts.RawAgentResponse{}, errors.New("map failed")
}

func TestNewRunner(t *testing.T) {
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: []string{"custom-acp", "--stdio"},
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, runner)
}

func TestNewRunnerCarriesMCPServers(t *testing.T) {
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: []string{"custom-acp", "--stdio"},
		},
	}
	mcpServers := map[string]agentconfig.MCPServerConfig{
		tasksMCPServerName: {
			Type: agentconfig.MCPServerTypeStdio,
			Cmd:  []string{"norma", "mcp", "tasks"},
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, mcpServers)
	require.NoError(t, err)

	typed, ok := runner.(*adkRunner)
	require.True(t, ok)
	require.Len(t, typed.mcpServers, 1)
	assert.Equal(t, mcpServers[tasksMCPServerName], typed.mcpServers[tasksMCPServerName])
}

func TestAinvokeRunner_Run(t *testing.T) {
	workingDir, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(workingDir) }()

	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommand(t, `{"status":"ok","summary":"success"}`),
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(workingDir)

	ctx := context.Background()
	var events bytes.Buffer
	resp, exitCode, err := runner.Run(ctx, reqJSON, io.Discard, io.Discard, &events)
	assert.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "ok", resp.Status)
	assert.NotEmpty(t, events.String())

	eventLines := parseJSONLines(t, events.Bytes())
	require.NotEmpty(t, eventLines)
	first := eventLines[0]
	assert.Equal(t, "event", first["type"])
	assert.NotEmpty(t, first["logged_at"])
	assert.NotNil(t, first["event"])
}

func TestAinvokeRunner_RunSetsSessionCWDAndExpandsPrompt(t *testing.T) {
	workingDir := t.TempDir()

	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommandWithEnv(t, `{"status":"ok","summary":"success"}`, map[string]string{
				"GO_EXPECT_SESSION_CWD":         workingDir,
				"GO_EXPECT_PROMPT_CONTAINS":     "cwd=" + workingDir,
				"GO_EXPECT_PROMPT_NOT_CONTAINS": "cwd={cwd}",
			}),
		},
	}

	runner, err := NewRunner(cfg, &cwdPromptRole{}, nil)
	require.NoError(t, err)

	resp, exitCode, err := runner.Run(context.Background(), runnerTestRequest(workingDir), io.Discard, io.Discard, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "ok", resp.Status)
}

func TestAinvokeRunner_RunHandlesChunkedStructuredOutput(t *testing.T) {
	workingDir, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(workingDir) }()

	response := "Let me inspect first.\n" +
		`{"status":"ok","summary":"success"}`
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommandChunked(t, response, 9),
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(workingDir)

	var events bytes.Buffer
	resp, exitCode, runErr := runner.Run(context.Background(), reqJSON, io.Discard, io.Discard, &events)
	require.NoError(t, runErr)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "success", resp.Summary)
}

func TestAinvokeRunner_RunRejectsTrailingContentAfterMarkdownFence(t *testing.T) {
	workingDir, err := os.MkdirTemp("", "norma-agent-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(workingDir) }()

	response := "Let me inspect first.\n" +
		`{"status":"ok","summary":"success"}` +
		"\n```\nextra"
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommandChunked(t, response, 7),
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(workingDir)

	var events bytes.Buffer
	_, exitCode, runErr := runner.Run(context.Background(), reqJSON, io.Discard, io.Discard, &events)
	require.Error(t, runErr)
	assert.NotEqual(t, 0, exitCode)
	assert.Contains(t, runErr.Error(), "validate structured output")
	assert.NotContains(t, runErr.Error(), "map agent response")
}

func TestAinvokeRunner_RunWritesErrorToStderr(t *testing.T) {
	// For ACP agents, errors are usually reported via the protocol or connection failure.
	// Here we simulate a connection failure (binary not found).
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: []string{"/non/existent/binary"},
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(t.TempDir())

	ctx := context.Background()
	var stderr bytes.Buffer
	_, exitCode, err := runner.Run(ctx, reqJSON, io.Discard, &stderr, io.Discard)
	assert.Error(t, err)
	assert.NotEqual(t, 0, exitCode)
}

func TestAinvokeRunner_RunReturnsErrorWhenResponseMappingFails(t *testing.T) {
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommand(t, "{}"),
		},
	}

	runner, err := NewRunner(cfg, &failingMapRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(t.TempDir())

	_, exitCode, err := runner.Run(context.Background(), reqJSON, io.Discard, io.Discard, io.Discard)
	require.Error(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, err.Error(), "map agent response")
	assert.Contains(t, err.Error(), "map failed")
}

func TestAinvokeRunner_RunWritesErrorEventLogOnPromptFailure(t *testing.T) {
	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommandWithPromptError(t, "prompt failed"),
		},
	}

	runner, err := NewRunner(cfg, &dummyRole{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(t.TempDir())

	var events bytes.Buffer
	_, exitCode, err := runner.Run(context.Background(), reqJSON, io.Discard, io.Discard, &events)
	require.Error(t, err)
	assert.NotEqual(t, 0, exitCode)

	lines := parseJSONLines(t, events.Bytes())
	require.NotEmpty(t, lines)

	last := lines[len(lines)-1]
	assert.Equal(t, "error", last["type"])
	assert.NotEmpty(t, last["logged_at"])
	errObj, ok := last["error"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, errObj["message"], "prompt failed")
	assert.NotEmpty(t, errObj["error_type"])
}

func helperACPCommand(t *testing.T, response string) []string {
	t.Helper()
	return helperACPCommandWithEnv(t, response, nil)
}

func helperACPCommandWithEnv(t *testing.T, response string, env map[string]string) []string {
	t.Helper()
	cmd := make([]string, 0, 6+len(env))
	cmd = append(cmd, "env", "GO_WANT_AGENT_ACP_HELPER=1", "GO_HELPER_RESPONSE="+response)
	for k, v := range env {
		cmd = append(cmd, k+"="+v)
	}
	return append(cmd,
		os.Args[0],
		"-test.run=TestAgentACPHelperProcess",
		"--",
	)
}

func helperACPCommandWithPromptError(t *testing.T, message string) []string {
	t.Helper()
	return []string{
		"env",
		"GO_WANT_AGENT_ACP_HELPER=1",
		"GO_HELPER_PROMPT_ERROR=" + message,
		os.Args[0],
		"-test.run=TestAgentACPHelperProcess",
		"--",
	}
}

func helperACPCommandChunked(t *testing.T, response string, chunkSize int) []string {
	t.Helper()
	return []string{
		"env",
		"GO_WANT_AGENT_ACP_HELPER=1",
		"GO_HELPER_RESPONSE=" + response,
		"GO_HELPER_CHUNK_SIZE=" + strconv.Itoa(chunkSize),
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
			expectedCWD := strings.TrimSpace(os.Getenv("GO_EXPECT_SESSION_CWD"))
			if expectedCWD != "" {
				var params struct {
					CWD string `json:"cwd"`
				}
				if err := json.Unmarshal(req.Params, &params); err == nil && params.CWD != expectedCWD {
					_ = encoder.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error": map[string]any{
							"code":    -32000,
							"message": fmt.Sprintf("unexpected session cwd: %q, want %q", params.CWD, expectedCWD),
						},
					})
					continue
				}
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessionId": "session-1",
				},
			})
		case acp.AgentMethodSessionPrompt:
			var params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(req.Params, &params)
			var prompt strings.Builder
			for _, block := range params.Prompt {
				prompt.WriteString(block.Text)
			}
			if want := os.Getenv("GO_EXPECT_PROMPT_CONTAINS"); want != "" && !strings.Contains(prompt.String(), want) {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    -32000,
						"message": fmt.Sprintf("prompt missing %q", want),
					},
				})
				continue
			}
			if unwanted := os.Getenv("GO_EXPECT_PROMPT_NOT_CONTAINS"); unwanted != "" && strings.Contains(prompt.String(), unwanted) {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    -32000,
						"message": fmt.Sprintf("prompt unexpectedly contains %q", unwanted),
					},
				})
				continue
			}
			if promptErr := strings.TrimSpace(os.Getenv("GO_HELPER_PROMPT_ERROR")); promptErr != "" {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    -32000,
						"message": promptErr,
					},
				})
				continue
			}
			responseText := os.Getenv("GO_HELPER_RESPONSE")
			chunkSize := 0
			if raw := strings.TrimSpace(os.Getenv("GO_HELPER_CHUNK_SIZE")); raw != "" {
				parsed, parseErr := strconv.Atoi(raw)
				if parseErr == nil && parsed > 0 {
					chunkSize = parsed
				}
			}

			if chunkSize <= 0 {
				emitACPTextChunk(encoder, responseText)
			} else {
				for start := 0; start < len(responseText); start += chunkSize {
					end := start + chunkSize
					if end > len(responseText) {
						end = len(responseText)
					}
					emitACPTextChunk(encoder, responseText[start:end])
				}
			}
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

func emitACPTextChunk(encoder *json.Encoder, text string) {
	if encoder == nil {
		return
	}
	_ = encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  acp.ClientMethodSessionUpdate,
		"params": map[string]any{
			"sessionId": "session-1",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": text,
				},
			},
		},
	})
}

func parseJSONLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := make([]map[string]any, 0)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(text), &line); err != nil {
			t.Fatalf("unmarshal json line %q: %v", text, err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan json lines: %v", err)
	}
	return lines
}

func TestRunnerWrapsErrorsWithPercentW(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("outer: %w", structuredagent.ErrStructuredOutputSchemaValidation)
	assert.True(t, errors.Is(err, structuredagent.ErrStructuredOutputSchemaValidation),
		"errors.Is should work through %%w wrapping")
	assert.True(t, errors.Is(err, structuredagent.ErrStructuredIOSchemaValidation),
		"errors.Is should work through %%w wrapping to umbrella error")

	err = fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", structuredagent.ErrStructuredIOSchemaValidation))
	assert.True(t, errors.Is(err, structuredagent.ErrStructuredIOSchemaValidation),
		"errors.Is should work through nested %%w wrapping")
}

type roleWithPlanOutput struct {
	dummyRole
}

func (r *roleWithPlanOutput) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var resp contracts.RawAgentResponse
	err := json.Unmarshal(outBytes, &resp)
	if err != nil {
		return resp, err
	}
	resp.PlanOutput = []byte(`{"acceptance_criteria":[{"id":"AC-1","text":"test","checks":[]}],"do_steps":[{"id":"DO-1","text":"test step"}]}`)
	return resp, nil
}

func TestAinvokeRunner_RunPreservesPlanOutput(t *testing.T) {
	workingDir, err := os.MkdirTemp("", "norma-pdca-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(workingDir) }()

	cfg := config.AgentConfig{
		Type: config.AgentTypeGenericACP,
		GenericACP: &agentconfig.ACPConfig{
			Cmd: helperACPCommand(t, `{"status":"ok","summary":"success"}`),
		},
	}

	runner, err := NewRunner(cfg, &roleWithPlanOutput{}, nil)
	require.NoError(t, err)

	reqJSON := runnerTestRequest(workingDir)

	ctx := context.Background()
	resp, exitCode, err := runner.Run(ctx, reqJSON, io.Discard, io.Discard, io.Discard)
	assert.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.PlanOutput, "plan_output should be preserved")
}
