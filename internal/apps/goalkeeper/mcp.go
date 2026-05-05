package goalkeeper

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

const (
	mcpServerName    = "norma-goalkeeper"
	mcpServerVersion = "1.0.0"
	runJobToolName   = "goalkeeper.run_job"
)

type jobRunner interface {
	RunJob(ctx context.Context, jobID string, role string, task string) (string, error)
}

type service struct {
	runner       jobRunner
	logger       zerolog.Logger
	maxToolCalls int
	mu           sync.Mutex
	callCount    int
}

func newService(runner jobRunner, logger zerolog.Logger, maxToolCalls int) *service {
	return &service{
		runner:       runner,
		logger:       logger,
		maxToolCalls: maxToolCalls,
	}
}

type runJobInput struct {
	JobID string `json:"job_id"`
	Role  string `json:"role"`
	Task  string `json:"task"`
}

type runJobOutput struct {
	JobID  string `json:"job_id"`
	Role   string `json:"role"`
	Status string `json:"status"`
	Result string `json:"result"`
}

func newMCPServer(service *service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: "Use goalkeeper.run_job to run one PDCA JOB on a Goalkeeper subagent."},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        runJobToolName,
		Description: "Run one JOB on a Goalkeeper PDCA subagent. Role must be plan, do, check, or act.",
	}, service.runJob)
	return server
}

func (s *service) runJob(ctx context.Context, _ *mcp.CallToolRequest, input runJobInput) (*mcp.CallToolResult, runJobOutput, error) {
	jobID := strings.TrimSpace(input.JobID)
	role := strings.ToLower(strings.TrimSpace(input.Role))
	task := strings.TrimSpace(input.Task)
	if jobID == "" {
		return toolError("job_id is required", runJobOutput{Status: "error"}), runJobOutput{Status: "error"}, nil
	}
	if _, ok := pdcaRoles[role]; !ok {
		out := runJobOutput{JobID: jobID, Role: role, Status: "error", Result: fmt.Sprintf("unknown role %q", input.Role)}
		return toolError(out.Result, out), out, nil
	}
	if task == "" {
		out := runJobOutput{JobID: jobID, Role: role, Status: "error", Result: "task is required"}
		return toolError(out.Result, out), out, nil
	}
	if !s.reserveCall() {
		out := runJobOutput{JobID: jobID, Role: role, Status: "error", Result: "max tool calls exceeded"}
		return toolError(out.Result, out), out, nil
	}

	s.logger.Debug().
		Str("job_id", jobID).
		Str("role", role).
		Msg("job dispatch")
	result, err := s.runner.RunJob(ctx, jobID, role, task)
	if err != nil {
		out := runJobOutput{JobID: jobID, Role: role, Status: "error", Result: err.Error()}
		s.logger.Debug().
			Err(err).
			Str("job_id", jobID).
			Str("role", role).
			Str("status", out.Status).
			Str("result", out.Result).
			Msg("job error")
		return toolError(out.Result, out), out, nil
	}
	out := runJobOutput{JobID: jobID, Role: role, Status: "completed", Result: strings.TrimSpace(result)}
	s.logger.Debug().
		Str("job_id", jobID).
		Str("role", role).
		Str("status", out.Status).
		Str("result", out.Result).
		Msg("job completed")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out.Result}},
	}, out, nil
}

func (s *service) reserveCall() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxToolCalls > 0 && s.callCount >= s.maxToolCalls {
		return false
	}
	s.callCount++
	return true
}

func toolError(message string, _ runJobOutput) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: message},
		},
	}
}
