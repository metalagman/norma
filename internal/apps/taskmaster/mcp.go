package taskmaster

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
	mcpServerName        = "norma-taskmaster"
	mcpServerVersion     = "1.0.0"
	scheduleTaskToolName = "taskmaster.schedule_task"
	finishToolName       = "taskmaster.finish"
)

type service struct {
	logger       zerolog.Logger
	maxToolCalls int
	coordinator  *coordinator

	mu        sync.Mutex
	callCount int
}

type scheduleTaskInput struct {
	TaskID  string       `json:"task_id"`
	Locator taskLocator  `json:"locator"`
	ReplyTo *taskLocator `json:"reply_to,omitempty"`
	Task    string       `json:"task"`
}

type scheduleTaskOutput struct {
	TaskID  string      `json:"task_id"`
	Locator taskLocator `json:"locator"`
	ReplyTo taskLocator `json:"reply_to"`
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
}

type finishInput struct {
	Summary string `json:"summary"`
}

type finishOutput struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func newService(logger zerolog.Logger, maxToolCalls int) *service {
	return &service{
		logger:       logger,
		maxToolCalls: maxToolCalls,
	}
}

func newMCPServer(service *service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: "Use taskmaster.schedule_task to enqueue one child-agent task and taskmaster.finish to finish the Taskmaster async run."},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        scheduleTaskToolName,
		Description: "Enqueue one child-agent task addressed by locator. Returns immediately after queueing.",
	}, service.scheduleTask)
	mcp.AddTool(server, &mcp.Tool{
		Name:        finishToolName,
		Description: "Finish the Taskmaster async run and return the final summary.",
	}, service.finish)
	return server
}

func startHTTPServer(ctx context.Context, service *service, addr string) (*httpServerResult, error) {
	return startGenericHTTPServer(ctx, addr, func(_ *http.Request) *mcp.Server {
		return newMCPServer(service)
	})
}

func (s *service) scheduleTask(ctx context.Context, _ *mcp.CallToolRequest, input scheduleTaskInput) (*mcp.CallToolResult, scheduleTaskOutput, error) {
	taskID := strings.TrimSpace(input.TaskID)
	locator, locatorErr := normalizeLocator(input.Locator)
	replyTo, replyErr := normalizeReplyLocator(input.ReplyTo)
	taskText := strings.TrimSpace(input.Task)
	if !s.reserveCall() {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: "max tool calls exceeded"}
		return toolError(out.Message), out, nil
	}
	if s.coordinator == nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: "coordinator is not ready"}
		return toolError(out.Message), out, nil
	}
	if locatorErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, Status: "error", Message: locatorErr.Error()}
		return toolError(out.Message), out, nil
	}
	if replyErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, Status: "error", Message: replyErr.Error()}
		return toolError(out.Message), out, nil
	}
	if err := s.coordinator.scheduleTask(taskID, locator, replyTo, taskText); err != nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, ReplyTo: replyTo, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}
	out := scheduleTaskOutput{
		TaskID:  taskID,
		Locator: locator,
		ReplyTo: replyTo,
		Status:  string(taskStatusQueued),
		Message: "task queued",
	}
	s.logger.Debug().
		Str("task_id", taskID).
		Interface("locator", locator).
		Interface("reply_to", replyTo).
		Msg("schedule_task accepted")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out.Message}},
	}, out, nil
}

func (s *service) finish(_ context.Context, _ *mcp.CallToolRequest, input finishInput) (*mcp.CallToolResult, finishOutput, error) {
	summary := strings.TrimSpace(input.Summary)
	if !s.reserveCall() {
		out := finishOutput{Status: "error", Summary: "max tool calls exceeded"}
		return toolError(out.Summary), out, nil
	}
	if s.coordinator == nil {
		out := finishOutput{Status: "error", Summary: "coordinator is not ready"}
		return toolError(out.Summary), out, nil
	}
	if err := s.coordinator.finish(summary); err != nil {
		out := finishOutput{Status: "error", Summary: err.Error()}
		return toolError(out.Summary), out, nil
	}
	out := finishOutput{Status: "finished", Summary: summary}
	s.logger.Debug().Str("summary", summary).Msg("finish accepted")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("finished: %s", summary)}},
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

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
	}
}
