package taskmaster

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
	logger      zerolog.Logger
	coordinator *coordinator
}

type scheduleTaskInput struct {
	TaskID   string       `json:"task_id"`
	Locator  taskLocator  `json:"locator"`
	ReportTo *taskLocator `json:"report_to,omitempty"`
	Prompt   string       `json:"prompt"`
}

type scheduleTaskOutput struct {
	TaskID   string      `json:"task_id"`
	Locator  taskLocator `json:"locator"`
	ReportTo taskLocator `json:"report_to"`
	Status   string      `json:"status"`
	Message  string      `json:"message,omitempty"`
}

type finishInput struct {
	Summary string `json:"summary"`
}

type finishOutput struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func newService(logger zerolog.Logger) *service {
	return &service{logger: logger}
}

func newMCPServer(service *service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: "Use taskmaster.schedule_task to enqueue one child-agent task and taskmaster.finish to finish the Taskmaster async run."},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        scheduleTaskToolName,
		Description: "Enqueue one child-agent task addressed by locator using a plain-text prompt. Optionally set report_to for completion reporting. Returns immediately after queueing.",
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
	reportTo, reportErr := normalizeReportLocator(input.ReportTo)
	prompt := strings.TrimSpace(input.Prompt)
	if s.coordinator == nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, ReportTo: reportTo, Status: "error", Message: "coordinator is not ready"}
		return toolError(out.Message), out, nil
	}
	if locatorErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, Status: "error", Message: locatorErr.Error()}
		return toolError(out.Message), out, nil
	}
	if reportErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, Status: "error", Message: reportErr.Error()}
		return toolError(out.Message), out, nil
	}
	if err := s.coordinator.scheduleTask(taskID, locator, reportTo, prompt); err != nil {
		out := scheduleTaskOutput{TaskID: taskID, Locator: locator, ReportTo: reportTo, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}
	out := scheduleTaskOutput{
		TaskID:   taskID,
		Locator:  locator,
		ReportTo: reportTo,
		Status:   string(taskStatusQueued),
		Message:  "task queued",
	}
	s.logger.Debug().
		Str("task_id", taskID).
		Interface("locator", locator).
		Interface("report_to", reportTo).
		Msg("schedule_task accepted")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out.Message}},
	}, out, nil
}

func (s *service) finish(_ context.Context, _ *mcp.CallToolRequest, input finishInput) (*mcp.CallToolResult, finishOutput, error) {
	summary := strings.TrimSpace(input.Summary)
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

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
	}
}
