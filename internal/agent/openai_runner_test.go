package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metalagman/norma/internal/agents/pdca/models"
	"github.com/metalagman/norma/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIRunner_Run(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"error": {"code": "", "message": ""},
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [
						{
							"type": "output_text",
							"text": "{\"status\":\"ok\",\"summary\":{\"text\":\"success\"},\"progress\":{\"title\":\"done\",\"details\":[]}}",
							"annotations": []
						}
					]
				}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	runner, err := NewRunner(config.AgentConfig{
		Type:    config.AgentTypeOpenAI,
		Model:   "gpt-5",
		BaseURL: srv.URL,
		APIKey:  "test-key",
	}, &dummyRole{})
	require.NoError(t, err)

	req := models.AgentRequest{
		Paths: models.RequestPaths{
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

	var resp models.AgentResponse
	require.NoError(t, json.Unmarshal(outBytes, &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.Equal(t, "success", resp.Summary.Text)
}
