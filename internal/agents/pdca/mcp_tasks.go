package pdca

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/v2/internal/apps/sessionmcp"
	"github.com/normahq/norma/v2/internal/apps/tasksmcp"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/normahq/runtime/v2/agentconfig"
)

const (
	tasksMCPServerName   = "norma_tasks"
	sessionMCPServerName = "norma_state"
	sessionMCPToolPrefix = "state"
)

var resolveExecutablePath = os.Executable

// embeddedMCPServers holds the embedded server results for cleanup.
type embeddedMCPServers struct {
	TaskServer  *tasksmcp.HTTPServerResult
	StateServer *sessionmcp.HTTPServerResult
}

// startEmbeddedMCPServers starts both the tasks and state MCP servers for inter-process communication.
// Returns the embedded servers for cleanup.
func startEmbeddedMCPServers(ctx context.Context, workingDir string) (*embeddedMCPServers, map[string]agentconfig.MCPServerConfig, error) {
	trimmedWorkingDir := strings.TrimSpace(workingDir)
	if trimmedWorkingDir == "" {
		return nil, nil, fmt.Errorf("working directory is required")
	}

	absoluteWorkingDir, err := filepath.Abs(trimmedWorkingDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve working directory path %q: %w", trimmedWorkingDir, err)
	}

	// Start tasks MCP server
	tracker := task.NewBeadsTracker("")
	tracker.WorkingDir = absoluteWorkingDir

	taskServer, err := tasksmcp.StartHTTPServer(ctx, tracker, "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("start tasks MCP server: %w", err)
	}

	// Start state MCP server
	stateServer, err := sessionmcp.StartHTTPServer(
		ctx,
		sessionmcp.NewMemoryStore(),
		"127.0.0.1:0",
		sessionmcp.WithToolPrefix(sessionMCPToolPrefix),
	)
	if err != nil {
		_ = taskServer.Close()
		return nil, nil, fmt.Errorf("start state MCP server: %w", err)
	}

	mcpServers := map[string]agentconfig.MCPServerConfig{
		tasksMCPServerName: {
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  fmt.Sprintf("http://%s", taskServer.Addr),
		},
		sessionMCPServerName: {
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  fmt.Sprintf("http://%s", stateServer.Addr),
		},
	}

	return &embeddedMCPServers{
		TaskServer:  taskServer,
		StateServer: stateServer,
	}, mcpServers, nil
}

// close stops all embedded MCP servers.
func (e *embeddedMCPServers) close() error {
	var firstErr error
	if e.TaskServer != nil {
		if err := e.TaskServer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if e.StateServer != nil {
		if err := e.StateServer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func roleMCPServers(roleName, workingDir string) (map[string]agentconfig.MCPServerConfig, error) {
	if strings.TrimSpace(roleName) != RolePlan {
		return nil, nil
	}

	trimmedWorkingDir := strings.TrimSpace(workingDir)
	if trimmedWorkingDir == "" {
		return nil, fmt.Errorf("working directory is required for %q role MCP tasks server", RolePlan)
	}

	absoluteWorkingDir, err := filepath.Abs(trimmedWorkingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory path %q: %w", trimmedWorkingDir, err)
	}

	executablePath, err := resolveExecutablePath()
	if err != nil {
		return nil, fmt.Errorf("resolve norma executable path: %w", err)
	}
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return nil, fmt.Errorf("resolved empty norma executable path")
	}

	return map[string]agentconfig.MCPServerConfig{
		tasksMCPServerName: {
			Type: agentconfig.MCPServerTypeStdio,
			Cmd:  []string{executablePath, "mcp", "tasks", "--working-dir", absoluteWorkingDir},
		},
	}, nil
}
