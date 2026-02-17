package execmodel_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/metalagman/norma/internal/adk/execmodel"
	"github.com/stretchr/testify/require"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestIntegration_LLMAgent(t *testing.T) {
	runDir, err := os.MkdirTemp("", "execmodel-integration-*")
	require.NoError(t, err)
	defer os.RemoveAll(runDir)

	scriptPath := filepath.Join(runDir, "mock_agent.sh")
	scriptContent := `#!/bin/sh
# ainvoke writes input.json to RunDir
# we just produce the expected output.json
echo '{"output": "integrated response"}' > output.json
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	cfg := execmodel.Config{
		RunDir: runDir,
		Cmd:    []string{scriptPath},
	}

	m, err := execmodel.New(cfg)
	require.NoError(t, err)

	a, err := llmagent.New(llmagent.Config{
		Name:        "test-agent",
		Description: "testing integration",
		Model:       m,
		Instruction: "you are a helpful assistant",
	})
	require.NoError(t, err)

	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "test-app",
		Agent:          a,
		SessionService: sessionService,
	})
	require.NoError(t, err)

	ctx := context.Background()
	userID := "test-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  userID,
	})
	require.NoError(t, err)

	userContent := genai.NewContentFromText("hello", genai.RoleUser)
	events := r.Run(ctx, userID, sess.Session.ID(), userContent, agent.RunConfig{})

	found := false
	for ev, err := range events {
		require.NoError(t, err)
		if ev.Content != nil {
			require.Len(t, ev.Content.Parts, 1)
			require.Equal(t, "integrated response", ev.Content.Parts[0].Text)
			found = true
		}
	}
	require.True(t, found, "should have received a response from the agent")
}
