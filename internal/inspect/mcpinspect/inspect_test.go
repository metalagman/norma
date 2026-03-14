package mcpinspect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunJSONIncludesCapabilitiesAndTools(t *testing.T) {
	wrapper := writeMCPWrapper(t, "full")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), RunConfig{
		Command:    []string{wrapper, "mcp-server"},
		WorkingDir: t.TempDir(),
		JSONOutput: true,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var got struct {
		Initialize *struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"initialize"`
		Tools []struct {
			Name         string `json:"name"`
			InputSchema  any    `json:"inputSchema"`
			OutputSchema any    `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout.String())
	}
	if got.Initialize == nil || got.Initialize.Capabilities == nil {
		t.Fatalf("initialize.capabilities missing in output: %s", stdout.String())
	}
	if len(got.Tools) == 0 {
		t.Fatalf("tools should not be empty: %s", stdout.String())
	}
	if got.Tools[0].Name != "echo" {
		t.Fatalf("tools[0].name = %q, want %q", got.Tools[0].Name, "echo")
	}
	if got.Tools[0].InputSchema == nil {
		t.Fatalf("tools[0].inputSchema must not be nil")
	}
	if got.Tools[0].OutputSchema == nil {
		t.Fatalf("tools[0].outputSchema must not be nil")
	}
}

func TestRunHumanOutputIncludesCapabilitiesAndTools(t *testing.T) {
	wrapper := writeMCPWrapper(t, "full")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), RunConfig{
		Command:    []string{wrapper, "mcp-server", "--trace"},
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Capabilities:",
		"Tools (1):",
		"Parameters:",
		"Response:",
		"- echo:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want substring %q", output, want)
		}
	}
}

func TestRunToolsOnlyMarksUnsupportedFeatures(t *testing.T) {
	wrapper := writeMCPWrapper(t, "tools-only")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), RunConfig{
		Command:    []string{wrapper, "mcp-server"},
		WorkingDir: t.TempDir(),
		Stdout:     &stdout,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Tools (1):",
		"Prompts: unsupported",
		"Resources: unsupported",
		"Resource templates: unsupported",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func writeMCPWrapper(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	wrapperPath := filepath.Join(dir, "mcp-wrapper.sh")
	script := fmt.Sprintf(`#!/bin/sh
exec env GO_WANT_MCP_INSPECT_HELPER=1 MCP_INSPECT_HELPER_MODE=%s %s -test.run=TestMCPInspectHelperProcess -- "$@"
`, shellQuote(mode), shellQuote(os.Args[0]))
	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", wrapperPath, err)
	}
	return wrapperPath
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestMCPInspectHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_INSPECT_HELPER") != "1" {
		return
	}
	if err := runMCPInspectHelper(context.Background(), os.Getenv("MCP_INSPECT_HELPER_MODE")); err != nil {
		t.Fatalf("runMCPInspectHelper() error = %v", err)
	}
	os.Exit(0)
}

func runMCPInspectHelper(ctx context.Context, mode string) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-inspect-helper", Version: "v1.0.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Echoes text input"}, func(_ context.Context, _ *mcp.CallToolRequest, input helperEchoInput) (*mcp.CallToolResult, helperEchoOutput, error) {
		return nil, helperEchoOutput{Echo: input.Text}, nil
	})

	if mode != "tools-only" {
		server.AddPrompt(&mcp.Prompt{
			Name:        "greet",
			Description: "Greets the provided name",
			Arguments: []*mcp.PromptArgument{
				{Name: "name", Description: "Name to greet", Required: true},
			},
		}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			name := req.Params.Arguments["name"]
			return &mcp.GetPromptResult{
				Description: "Greeting prompt",
				Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "Hello " + name}},
				},
			}, nil
		})
		server.AddResource(&mcp.Resource{
			URI:         "file:///inspect/info.txt",
			Name:        "inspect-info",
			Description: "Inspect info resource",
			MIMEType:    "text/plain",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      req.Params.URI,
						MIMEType: "text/plain",
						Text:     "inspect resource",
					},
				},
			}, nil
		})
		server.AddResourceTemplate(&mcp.ResourceTemplate{
			Name:        "inspect-template",
			URITemplate: "file:///inspect/{name}",
			Description: "Inspect resource template",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      req.Params.URI,
						MIMEType: "text/plain",
						Text:     "inspect template resource",
					},
				},
			}, nil
		})
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

type helperEchoInput struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

type helperEchoOutput struct {
	Echo string `json:"echo" jsonschema:"echoed text"`
}
