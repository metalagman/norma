package goalkeeper

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpServerName    = "norma-goalkeeper"
	mcpServerVersion = "1.0.0"
	runJobToolName   = "goalkeeper.run_job"
)

type jobRunner interface {
	RunJob(ctx context.Context, role string, task string) (string, error)
}

type service struct {
	runner       jobRunner
	transcript   io.Writer
	maxToolCalls int
	mu           sync.Mutex
	callCount    int
}

func newService(runner jobRunner, transcript io.Writer, maxToolCalls int) *service {
	return &service{
		runner:       runner,
		transcript:   transcript,
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

	s.writeTranscriptf("job %s: dispatch role=%s\n", jobID, role)
	result, err := s.runner.RunJob(ctx, role, task)
	if err != nil {
		out := runJobOutput{JobID: jobID, Role: role, Status: "error", Result: err.Error()}
		s.writeTranscriptf("job %s: error %s\n", jobID, err.Error())
		return toolError(out.Result, out), out, nil
	}
	out := runJobOutput{JobID: jobID, Role: role, Status: "completed", Result: strings.TrimSpace(result)}
	s.writeTranscriptf("job %s: completed role=%s result=%s\n", jobID, role, out.Result)
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

func (s *service) writeTranscriptf(format string, args ...any) {
	if s.transcript == nil {
		return
	}
	_, _ = fmt.Fprintf(s.transcript, format, args...)
}

func toolError(message string, _ runJobOutput) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: message},
		},
	}
}
