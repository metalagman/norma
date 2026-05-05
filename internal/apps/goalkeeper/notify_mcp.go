package goalkeeper

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

const (
	scheduleJobToolName = "goalkeeper.schedule_job"
	finishToolName      = "goalkeeper.finish"
)

type notifyService struct {
	logger       zerolog.Logger
	maxToolCalls int
	coordinator  *notifyCoordinator

	mu        sync.Mutex
	callCount int
}

type scheduleJobInput struct {
	JobID   string      `json:"job_id"`
	Locator jobLocator  `json:"locator"`
	ReplyTo *jobLocator `json:"reply_to,omitempty"`
	Task    string      `json:"task"`
}

type scheduleJobOutput struct {
	JobID   string     `json:"job_id"`
	Locator jobLocator `json:"locator"`
	ReplyTo jobLocator `json:"reply_to"`
	Status  string     `json:"status"`
	Message string     `json:"message,omitempty"`
}

type finishInput struct {
	Summary string `json:"summary"`
}

type finishOutput struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func newNotifyService(logger zerolog.Logger, maxToolCalls int) *notifyService {
	return &notifyService{
		logger:       logger,
		maxToolCalls: maxToolCalls,
	}
}

func newNotifyMCPServer(service *notifyService) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: "Use goalkeeper.schedule_job to enqueue one child-agent job and goalkeeper.finish to finish the Goalkeeper async run."},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        scheduleJobToolName,
		Description: "Enqueue one child-agent JOB addressed by locator. Returns immediately after queueing.",
	}, service.scheduleJob)
	mcp.AddTool(server, &mcp.Tool{
		Name:        finishToolName,
		Description: "Finish the Goalkeeper async run and return the final summary.",
	}, service.finish)
	return server
}

func startNotifyHTTPServer(ctx context.Context, service *notifyService, addr string) (*httpServerResult, error) {
	return startGenericHTTPServer(ctx, addr, func(_ *http.Request) *mcp.Server {
		return newNotifyMCPServer(service)
	})
}

func (s *notifyService) scheduleJob(ctx context.Context, _ *mcp.CallToolRequest, input scheduleJobInput) (*mcp.CallToolResult, scheduleJobOutput, error) {
	jobID := strings.TrimSpace(input.JobID)
	locator, locatorErr := normalizeLocator(input.Locator)
	replyTo, replyErr := normalizeReplyLocator(input.ReplyTo)
	task := strings.TrimSpace(input.Task)
	if !s.reserveCall() {
		out := scheduleJobOutput{JobID: jobID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: "max tool calls exceeded"}
		return toolError(out.Message, runJobOutput{}), out, nil
	}
	if s.coordinator == nil {
		out := scheduleJobOutput{JobID: jobID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: "coordinator is not ready"}
		return toolError(out.Message, runJobOutput{}), out, nil
	}
	if locatorErr != nil {
		out := scheduleJobOutput{JobID: jobID, Status: "error", Message: locatorErr.Error()}
		return toolError(out.Message, runJobOutput{}), out, nil
	}
	if replyErr != nil {
		out := scheduleJobOutput{JobID: jobID, Locator: locator, Status: "error", Message: replyErr.Error()}
		return toolError(out.Message, runJobOutput{}), out, nil
	}
	if err := s.coordinator.scheduleJob(jobID, locator, replyTo, task); err != nil {
		out := scheduleJobOutput{JobID: jobID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: err.Error()}
		return toolError(out.Message, runJobOutput{}), out, nil
	}
	out := scheduleJobOutput{
		JobID:   jobID,
		Locator: locator,
		ReplyTo: replyTo,
		Status:  string(notifyJobStatusQueued),
		Message: "job queued",
	}
	s.logger.Debug().
		Str("job_id", jobID).
		Interface("locator", locator).
		Interface("reply_to", replyTo).
		Msg("schedule_job accepted")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out.Message}},
	}, out, nil
}

func (s *notifyService) finish(_ context.Context, _ *mcp.CallToolRequest, input finishInput) (*mcp.CallToolResult, finishOutput, error) {
	summary := strings.TrimSpace(input.Summary)
	if !s.reserveCall() {
		out := finishOutput{Status: "error", Summary: "max tool calls exceeded"}
		return toolError(out.Summary, runJobOutput{}), out, nil
	}
	if s.coordinator == nil {
		out := finishOutput{Status: "error", Summary: "coordinator is not ready"}
		return toolError(out.Summary, runJobOutput{}), out, nil
	}
	if err := s.coordinator.finish(summary); err != nil {
		out := finishOutput{Status: "error", Summary: err.Error()}
		return toolError(out.Summary, runJobOutput{}), out, nil
	}
	out := finishOutput{Status: "finished", Summary: summary}
	s.logger.Debug().Str("summary", summary).Msg("finish accepted")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("finished: %s", summary)}},
	}, out, nil
}

func (s *notifyService) reserveCall() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxToolCalls > 0 && s.callCount >= s.maxToolCalls {
		return false
	}
	s.callCount++
	return true
}
