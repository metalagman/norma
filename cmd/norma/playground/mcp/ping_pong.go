package mcpcmd

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func PingPongCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "ping-pong",
		Short:        "Run a ping-pong MCP server over stdio",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPingPongServer(cmd.Context())
		},
	}
}

type pingInput struct {
	Message string `json:"message"`
}

type pingOutput struct {
	Reply string `json:"reply"`
}

func runPingPongServer(ctx context.Context) error {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "norma-playground-ping-pong",
			Version: "1.0.0",
		},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Responds with pong and the original message",
	}, func(_ context.Context, req *mcp.CallToolRequest, input pingInput) (*mcp.CallToolResult, pingOutput, error) {
		if !hasMessageArgument(req) {
			return nil, pingOutput{}, errors.New(`"message" is required`)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "pong: " + input.Message},
			},
		}, pingOutput{Reply: "pong: " + input.Message}, nil
	})

	return server.Run(ctx, &mcp.StdioTransport{})
}

func hasMessageArgument(req *mcp.CallToolRequest) bool {
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
