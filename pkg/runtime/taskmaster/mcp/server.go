package mcp

import (
	"context"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

const ScheduleTaskToolName = "taskmaster.schedule_task"

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

func NewService(logger zerolog.Logger, defaultReportTo taskmaster.Locator) *Service {
	return &Service{
		logger:          logger,
		defaultReportTo: defaultReportTo,
	}
}

func (s *Service) SetController(controller Controller) {
	s.controller = controller
}

func RegisterTools(server *sdkmcp.Server, service *Service) {
	if server == nil || service == nil {
		return
	}
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        ScheduleTaskToolName,
		Description: "Enqueue one task addressed by locator using plain-text content. Session context is required. Optionally set report_to for async task-result routing. Returns immediately after queueing.",
	}, service.ScheduleTask)
}

func (s *Service) ScheduleTask(_ context.Context, _ *sdkmcp.CallToolRequest, input ScheduleTaskInput) (*sdkmcp.CallToolResult, ScheduleTaskOutput, error) {
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
