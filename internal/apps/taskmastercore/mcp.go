package taskmastercore

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
	logger          zerolog.Logger
	coordinator     *coordinator
	defaultReportTo Locator
	allowFinishTool bool
}

type scheduleTaskInput struct {
	TaskID    string   `json:"task_id"`
	SessionID string   `json:"session_id"`
	Locator   Locator  `json:"locator"`
	ReportTo  *Locator `json:"report_to,omitempty"`
	Prompt    string   `json:"prompt"`
}

type scheduleTaskOutput struct {
	TaskID    string  `json:"task_id"`
	SessionID string  `json:"session_id"`
	Locator   Locator `json:"locator"`
	ReportTo  Locator `json:"report_to"`
	Status    string  `json:"status"`
	Message   string  `json:"message,omitempty"`
}

type finishInput struct {
	Summary string `json:"summary"`
}

type finishOutput struct {
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func newService(logger zerolog.Logger, defaultReportTo Locator, allowFinishTool bool) *service {
	return &service{
		logger:          logger,
		defaultReportTo: defaultReportTo,
		allowFinishTool: allowFinishTool,
	}
}

func newMCPServer(service *service) *mcp.Server {
	instructions := "Use taskmaster.schedule_task to enqueue one task in the async run. Every scheduled task must include task_id, session_id, locator, optional report_to, and prompt."
	if service.allowFinishTool {
		instructions += " Use taskmaster.finish to finish the async run."
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        scheduleTaskToolName,
		Description: "Enqueue one task addressed by locator using a plain-text prompt. Session context is required. Optionally set report_to for completion reporting. Returns immediately after queueing.",
	}, service.scheduleTask)
	if service.allowFinishTool {
		mcp.AddTool(server, &mcp.Tool{
			Name:        finishToolName,
			Description: "Finish the async run and return the final summary.",
		}, service.finish)
	}
	return server
}

func startHTTPServer(ctx context.Context, service *service, addr string) (*httpServerResult, error) {
	return startGenericHTTPServer(ctx, addr, func(_ *http.Request) *mcp.Server {
		return newMCPServer(service)
	})
}

func (s *service) scheduleTask(ctx context.Context, _ *mcp.CallToolRequest, input scheduleTaskInput) (*mcp.CallToolResult, scheduleTaskOutput, error) {
	taskID := strings.TrimSpace(input.TaskID)
	sessionID := strings.TrimSpace(input.SessionID)
	locator, locatorErr := normalizeLocator(input.Locator)
	reportTo, reportErr := normalizeReportLocator(input.ReportTo, s.defaultReportTo)
	prompt := strings.TrimSpace(input.Prompt)
	if s.coordinator == nil {
		out := scheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, ReportTo: reportTo, Status: "error", Message: "coordinator is not ready"}
		return toolError(out.Message), out, nil
	}
	if locatorErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Status: "error", Message: locatorErr.Error()}
		return toolError(out.Message), out, nil
	}
	if reportErr != nil {
		out := scheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, Status: "error", Message: reportErr.Error()}
		return toolError(out.Message), out, nil
	}
	if err := s.coordinator.scheduleTask(taskID, sessionID, locator, reportTo, prompt); err != nil {
		out := scheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, ReportTo: reportTo, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}
	out := scheduleTaskOutput{
		TaskID:    taskID,
		SessionID: sessionID,
		Locator:   locator,
		ReportTo:  reportTo,
		Status:    string(taskStatusQueued),
		Message:   "task queued",
	}
	s.logger.Debug().
		Str("task_id", taskID).
		Str("session_id", sessionID).
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
