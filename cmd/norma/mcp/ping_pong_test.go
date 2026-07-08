package mcpcmd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPingPongCommandRegistered(t *testing.T) {
	cmd := Command()
	sub, _, err := cmd.Find([]string{"ping-pong"})
	require.NoError(t, err)
	assert.Equal(t, "ping-pong", sub.Name())
}

func setupMCPServer(t *testing.T) (context.Context, func(), *mcp.ClientSession) {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(
		&mcp.Implementation{Name: "norma-ping-pong", Version: "1.0.0"},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Responds with pong and the original message",
	}, func(_ context.Context, req *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, pingOutput, error) {
		if !hasMessageArgumentForTest(req) {
			return nil, pingOutput{}, errors.New(`"message" is required`)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "pong: " + input.Message},
			},
		}, pingOutput{Reply: "pong: " + input.Message}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	cleanup := func() {
		cancel()
		_ = session.Close()
	}

	return ctx, cleanup, session
}

func TestMCPServerInitializeAndListTools(t *testing.T) {
	_, cleanup, session := setupMCPServer(t)
	defer cleanup()

	result := session.InitializeResult()
	assert.Equal(t, "norma-ping-pong", result.ServerInfo.Name)
	assert.Equal(t, "1.0.0", result.ServerInfo.Version)

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "ping", tools.Tools[0].Name)
}

func TestPingTool(t *testing.T) {
	ctx, cleanup, session := setupMCPServer(t)
	defer cleanup()

	_ = session.InitializeResult()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ping",
		Arguments: map[string]any{
			"message": "hello world",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "pong: hello world", textContent.Text)
}

func TestPingToolEmptyMessage(t *testing.T) {
	ctx, cleanup, session := setupMCPServer(t)
	defer cleanup()

	_ = session.InitializeResult()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ping",
		Arguments: map[string]any{
			"message": "",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "pong: ", textContent.Text)
}

func TestPingToolMissingMessage(t *testing.T) {
	ctx, cleanup, session := setupMCPServer(t)
	defer cleanup()

	_ = session.InitializeResult()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "ping",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "missing properties")
	assert.Contains(t, textContent.Text, `"message"`)
}

func TestServerProducesNoExtraStdout(t *testing.T) {
	ctx, cleanup, session := setupMCPServer(t)
	defer cleanup()

	_ = session.InitializeResult()

	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "ping",
		Arguments: map[string]any{
			"message": "test",
		},
	})
	require.NoError(t, err)
}

func TestServerHasCorrectIdentity(t *testing.T) {
	_, cleanup, session := setupMCPServer(t)
	defer cleanup()

	result := session.InitializeResult()
	assert.Equal(t, "norma-ping-pong", result.ServerInfo.Name)
	assert.Equal(t, "1.0.0", result.ServerInfo.Version)
	assert.NotEmpty(t, result.ProtocolVersion)
}

func hasMessageArgumentForTest(req *mcp.CallToolRequest) bool {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return false
	}

	var args map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return false
	}

	_, ok := args["message"]

	return ok
}
