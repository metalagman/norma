package workspacemcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName     = "norma-workspace"
	serverVersion  = "1.0.0"
	defaultAddress = "127.0.0.1:9091"
)

type ToolError struct {
	Operation string `json:"operation"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type ToolOutcome struct {
	OK    bool       `json:"ok"`
	Error *ToolError `json:"error,omitempty"`
}

func okOutcome() ToolOutcome {
	return ToolOutcome{OK: true}
}

func validationFailure(operation string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "validation_error", message)
}

func backendFailure(operation string, err error) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "backend_error", err.Error())
}

func failure(operation string, code string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: message}},
		}, ToolOutcome{
			OK: false,
			Error: &ToolError{
				Operation: operation,
				Code:      code,
				Message:   message,
			},
		}
}

// WorkspaceService defines workspace sync operations.
type WorkspaceService interface {
	Import(ctx context.Context, sessionID string) error
	Export(ctx context.Context, sessionID string, commitMessage string) error
}

func Run(ctx context.Context, svc WorkspaceService) error {
	server, err := NewServer(svc)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func RunHTTP(ctx context.Context, svc WorkspaceService, addr string) error {
	result, err := StartHTTPServer(ctx, svc, addr)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return result.Close()
}

type HTTPServerResult struct {
	Addr  string
	Close func() error
}

func StartHTTPServer(ctx context.Context, svc WorkspaceService, addr string) (*HTTPServerResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}
	address := strings.TrimSpace(addr)
	if address == "" {
		address = defaultAddress
	}

	getServer := func(_ *http.Request) *mcp.Server {
		server, err := NewServer(svc)
		if err != nil {
			return nil
		}
		return server
	}

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", address, err)
	}

	actualAddr := listener.Addr().String()
	httpServer := &http.Server{Handler: handler}

	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	go func() {
		_ = httpServer.Serve(listener)
	}()

	return &HTTPServerResult{
		Addr: actualAddr,
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

func NewServer(svc WorkspaceService) (*mcp.Server, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    serverName,
			Version: serverVersion,
		},
		nil,
	)

	RegisterTools(server, svc)
	return server, nil
}

// RegisterTools adds workspace MCP tools to an existing server.
func RegisterTools(server *mcp.Server, svc WorkspaceService) {
	if server == nil || svc == nil {
		return
	}
	srv := &service{svc: svc}
	srv.registerTools(server)
}

type service struct {
	svc WorkspaceService
}

func (s *service) registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.workspace.import",
		Description: "Sync workspace branch with configured base branch (discarding uncommitted workspace changes before rebase)",
	}, s.importTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.workspace.export",
		Description: "Squash-merge workspace branch into configured base branch and commit",
	}, s.exportTool)
}

type importInput struct {
	SessionID string `json:"session_id" jsonschema:"relay session ID"`
}

type importOutput struct {
	ToolOutcome
}

func (s *service) importTool(ctx context.Context, _ *mcp.CallToolRequest, in importInput) (*mcp.CallToolResult, importOutput, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure("relay.workspace.import", "session_id is required")
		return result, importOutput{ToolOutcome: out}, nil
	}

	if err := s.svc.Import(ctx, in.SessionID); err != nil {
		result, out := backendFailure("relay.workspace.import", err)
		return result, importOutput{ToolOutcome: out}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Workspace synced to base branch successfully"}},
	}, importOutput{ToolOutcome: okOutcome()}, nil
}

type exportInput struct {
	SessionID     string `json:"session_id" jsonschema:"relay session ID"`
	CommitMessage string `json:"commit_message" jsonschema:"conventional commit message"`
}

type exportOutput struct {
	ToolOutcome
}

func (s *service) exportTool(ctx context.Context, _ *mcp.CallToolRequest, in exportInput) (*mcp.CallToolResult, exportOutput, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure("relay.workspace.export", "session_id is required")
		return result, exportOutput{ToolOutcome: out}, nil
	}
	if strings.TrimSpace(in.CommitMessage) == "" {
		result, out := validationFailure("relay.workspace.export", "commit_message is required")
		return result, exportOutput{ToolOutcome: out}, nil
	}

	if err := s.svc.Export(ctx, in.SessionID, in.CommitMessage); err != nil {
		result, out := backendFailure("relay.workspace.export", err)
		return result, exportOutput{ToolOutcome: out}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "Workspace exported to base branch successfully"}},
	}, exportOutput{ToolOutcome: okOutcome()}, nil
}
