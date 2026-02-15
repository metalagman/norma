package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/metalagman/norma/internal/agent/openaiapi"
	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCompletionClient struct {
	resp     openaiapi.CompletionResponse
	err      error
	requests []openaiapi.CompletionRequest
}

func (s *stubCompletionClient) Complete(_ context.Context, req openaiapi.CompletionRequest) (openaiapi.CompletionResponse, error) {
	s.requests = append(s.requests, req)
	return s.resp, s.err
}

func TestOpenAIRunner_Run_MissingAPIKeyDoesNotUseEnvFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key-should-not-be-used")

	_, err := NewRunner(config.AgentConfig{
		Type:    config.AgentTypeOpenAI,
		Model:   "gpt-5",
		BaseURL: "http://127.0.0.1",
		APIKey:  "   ",
	}, &dummyRole{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "openai api key is required")
}

func TestOpenAIRunner_Run(t *testing.T) {
	stubClient := &stubCompletionClient{
		resp: openaiapi.CompletionResponse{
			OutputText: `{"status":"ok","summary":{"text":"success"},"progress":{"title":"done","details":[]}}`,
		},
	}
	originalFactory := newOpenAICompletionClient
	var gotCfg openaiapi.Config
	newOpenAICompletionClient = func(cfg openaiapi.Config) (completionClient, error) {
		gotCfg = cfg
		return stubClient, nil
	}
	t.Cleanup(func() {
		newOpenAICompletionClient = originalFactory
	})

	runner, err := NewRunner(config.AgentConfig{
		Type:    config.AgentTypeOpenAI,
		Model:   "gpt-5",
		BaseURL: "https://api.example.test",
		APIKey:  "test-key",
	}, &dummyRole{})
	require.NoError(t, err)

	req := contracts.AgentRequest{
		Paths: contracts.RequestPaths{
			RunDir: t.TempDir(),
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	outBytes, _, exitCode, err := runner.Run(context.Background(), req, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), `"status":"ok"`)
	require.Len(t, stubClient.requests, 1)
	assert.Equal(t, "prompt", stubClient.requests[0].Instructions)
	assert.Equal(t, "gpt-5", gotCfg.Model)
	assert.Equal(t, "https://api.example.test", gotCfg.BaseURL)
	assert.Equal(t, "test-key", gotCfg.APIKey)

	var resp contracts.AgentResponse
	require.NoError(t, json.Unmarshal(outBytes, &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "success", resp.Summary.Text)
}
