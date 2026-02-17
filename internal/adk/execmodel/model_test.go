package execmodel_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/adk/execmodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestNew(t *testing.T) {
	cfg := execmodel.Config{}
	m, err := execmodel.New(cfg)
	assert.Error(t, err)
	assert.Nil(t, m)

	cfg.Cmd = []string{"echo"}
	m, err = execmodel.New(cfg)
	assert.NoError(t, err)
	assert.NotNil(t, m)
}

func TestModel_Name(t *testing.T) {
	cfg := execmodel.Config{}
	cfg.Cmd = []string{"my-model", "arg1"}
	m, err := execmodel.New(cfg)
	require.NoError(t, err)
	assert.Equal(t, "my-model", m.Name())
}

func TestModel_GenerateContent(t *testing.T) {
	runDir, err := os.MkdirTemp("", "execmodel-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(runDir)

	// Create a script that produces output.json as required by ainvoke
	// We use the default output schema format here
	scriptPath := filepath.Join(runDir, "mock_agent.sh")
	scriptContent := `#!/bin/sh
echo '{"output": "hello from exec"}' > output.json
`
	err = os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	require.NoError(t, err)

	cfg := execmodel.Config{
		RunDir: runDir,
		Cmd:    []string{scriptPath},
	}
	
	m, err := execmodel.New(cfg)
	require.NoError(t, err)

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("ping", genai.RoleUser),
		},
	}

	ctx := context.Background()
	seq := m.GenerateContent(ctx, req, false)
	
	count := 0
	for resp, err := range seq {
		if err != nil {
			t.Fatalf("GenerateContent failed: %v", err)
		}
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.Content)
		assert.Len(t, resp.Content.Parts, 1)
		assert.Equal(t, "hello from exec", resp.Content.Parts[0].Text)
		count++
	}
	assert.Equal(t, 1, count)
}

func TestModel_GenerateContent_WithOutputs(t *testing.T) {
	runDir, err := os.MkdirTemp("", "execmodel-outputs-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(runDir)

	scriptPath := filepath.Join(runDir, "mock_agent.sh")
	scriptContent := `#!/bin/sh
echo "to stdout"
echo "to stderr" >&2
echo '{"output": "ok"}' > output.json
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	var stdout, stderr bytes.Buffer
	cfg := execmodel.Config{
		RunDir: runDir,
		Cmd:    []string{scriptPath},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	m, err := execmodel.New(cfg)
	require.NoError(t, err)

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("hello", genai.RoleUser),
		},
	}

	ctx := context.Background()
	seq := m.GenerateContent(ctx, req, false)

	for _, err := range seq {
		require.NoError(t, err)
	}

	assert.Equal(t, "to stdout\n", stdout.String())
	assert.Equal(t, "to stderr\n", stderr.String())
}
