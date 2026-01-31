package agent

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopRunner_Run(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-loop-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	// Create a sub-agent that returns "ok"
	subCfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{"sh", "-c", `echo '{"status":"ok","summary":{"text":"iteration success"}}' > output.json && echo '{"status":"ok","summary":{"text":"iteration success"}}'`},
	}

	cfg := config.AgentConfig{
		Type:          "loop",
		MaxIterations: 2,
		SubAgents:     []config.AgentConfig{subCfg},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := models.AgentRequest{
		Run:  models.RunInfo{ID: "run-1", Iteration: 1},
		Step: models.StepInfo{Index: 1, Name: "do"},
		Paths: models.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
	}

	ctx := context.Background()
	stdout, _, exitCode, err := runner.Run(ctx, req, io.Discard, io.Discard)
	assert.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, string(stdout), "ADK Loop completed")
}

func TestLoopRunner_Escalate(t *testing.T) {
	repoRoot, err := os.MkdirTemp("", "norma-loop-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(repoRoot) }()

	// Create a sub-agent that returns "escalate": true
	subCfg := config.AgentConfig{
		Type: "exec",
		Cmd:  []string{"sh", "-c", `echo '{"status":"ok","escalate":true,"summary":{"text":"stop me"}}' > output.json && echo '{"status":"ok","escalate":true,"summary":{"text":"stop me"}}'`},
	}

	cfg := config.AgentConfig{
		Type:          "loop",
		MaxIterations: 5,
		SubAgents:     []config.AgentConfig{subCfg},
	}

	runner, err := NewRunner(cfg, &dummyRole{})
	require.NoError(t, err)

	req := models.AgentRequest{
		Run:  models.RunInfo{ID: "run-1", Iteration: 1},
		Step: models.StepInfo{Index: 1, Name: "do"},
		Paths: models.RequestPaths{
			WorkspaceDir: repoRoot,
			RunDir:       repoRoot,
		},
	}

	ctx := context.Background()
	stdout, _, exitCode, err := runner.Run(ctx, req, io.Discard, io.Discard)
	assert.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, string(stdout), `"escalate":true`)
	assert.NotContains(t, string(stdout), "Loop completed 5 iterations")
}
