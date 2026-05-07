package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

const (
	mcpServerName        = "norma-taskmaster"
	mcpServerVersion     = "1.0.0"
	ScheduleTaskToolName = "taskmaster.schedule_task"
)

type Controller interface {
	Enqueue(task taskmaster.Task) error
}

type Service struct {
	logger          zerolog.Logger
	controller      Controller
	defaultReportTo taskmaster.Locator
}

type ScheduleTaskInput struct {
	TaskID    string              `json:"task_id"`
	SessionID string              `json:"session_id"`
	Locator   taskmaster.Locator  `json:"locator"`
	ReportTo  *taskmaster.Locator `json:"report_to,omitempty"`
	Content   string              `json:"content"`
}

type ScheduleTaskOutput struct {
	TaskID    string             `json:"task_id"`
	SessionID string             `json:"session_id"`
	Locator   taskmaster.Locator `json:"locator"`
	ReportTo  taskmaster.Locator `json:"report_to"`
	Status    string             `json:"status"`
	Message   string             `json:"message,omitempty"`
}

type HTTPServer struct {
	Addr       string
	httpServer *http.Server
}

func NewService(logger zerolog.Logger, defaultReportTo taskmaster.Locator) *Service {
	return &Service{
		logger:          logger,
		defaultReportTo: defaultReportTo,
	}
}

func (s *Service) SetController(controller Controller) {
	s.controller = controller
}

func NewServer(service *Service) *sdkmcp.Server {
	instructions := "Use taskmaster.schedule_task to enqueue one task in the async run. Every scheduled task must include task_id, session_id, locator, optional report_to, and content."
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&sdkmcp.ServerOptions{Instructions: instructions},
	)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        ScheduleTaskToolName,
		Description: "Enqueue one task addressed by locator using plain-text content. Session context is required. Optionally set report_to for async task-result routing. Returns immediately after queueing.",
	}, service.scheduleTask)
	return server
}

func StartHTTPServer(ctx context.Context, service *Service, addr string) (*HTTPServer, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}
	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server {
		return NewServer(service)
	}, &sdkmcp.StreamableHTTPOptions{})
	httpServer := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return &HTTPServer{
		Addr:       listener.Addr().String(),
		httpServer: httpServer,
	}, nil
}

func (s *Service) scheduleTask(_ context.Context, _ *sdkmcp.CallToolRequest, input ScheduleTaskInput) (*sdkmcp.CallToolResult, ScheduleTaskOutput, error) {
	taskID := strings.TrimSpace(input.TaskID)
	sessionID := strings.TrimSpace(input.SessionID)
	locator, locatorErr := taskmaster.NormalizeLocator(input.Locator)
	reportTo, reportErr := normalizeReportTo(input.ReportTo, s.defaultReportTo)
	content := strings.TrimSpace(input.Content)
	if s.controller == nil {
		out := ScheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, ReportTo: reportTo, Status: "error", Message: "controller is not ready"}
		return toolError(out.Message), out, nil
	}
	if locatorErr != nil {
		out := ScheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Status: "error", Message: locatorErr.Error()}
		return toolError(out.Message), out, nil
	}
	if reportErr != nil {
		out := ScheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, Status: "error", Message: reportErr.Error()}
		return toolError(out.Message), out, nil
	}
	if err := s.controller.Enqueue(taskmaster.Task{
		ID:        taskID,
		SessionID: sessionID,
		Locator:   locator,
		ReportTo:  &reportTo,
		Content:   content,
	}); err != nil {
		out := ScheduleTaskOutput{TaskID: taskID, SessionID: sessionID, Locator: locator, ReportTo: reportTo, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}
	out := ScheduleTaskOutput{
		TaskID:    taskID,
		SessionID: sessionID,
		Locator:   locator,
		ReportTo:  reportTo,
		Status:    "queued",
		Message:   "task queued",
	}
	s.logger.Debug().
		Str("task_id", taskID).
		Str("session_id", sessionID).
		Str("locator", locator.String()).
		Str("report_to", reportTo.String()).
		Msg("schedule_task accepted")
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: out.Message}},
	}, out, nil
}

func (s *HTTPServer) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}

func normalizeReportTo(reportTo *taskmaster.Locator, defaultReportTo taskmaster.Locator) (taskmaster.Locator, error) {
	if reportTo == nil {
		return defaultReportTo, nil
	}
	return taskmaster.NormalizeLocator(*reportTo)
}

func toolError(message string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: message}},
	}
}
