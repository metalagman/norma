package pdcataskmaster

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

const (
	scheduleTaskToolName = "taskmaster.schedule_task"
	finishToolName       = "taskmaster.finish"
)

type scheduleController interface {
	Enqueue(msg taskmasterrt.Message) error
}

type scheduleService struct {
	logger     zerolog.Logger
	controller scheduleController
}

type scheduleTaskInput struct {
	SessionID string `json:"session_id"`
	Target    string `json:"target"`
	Content   string `json:"content"`
}

type scheduleTaskOutput struct {
	SessionID string `json:"session_id"`
	Target    string `json:"target"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

func newScheduleService(logger zerolog.Logger) *scheduleService {
	return &scheduleService{logger: logger}
}

func (s *scheduleService) SetController(controller scheduleController) {
	s.controller = controller
}

func registerControlTools(server *sdkmcp.Server, service *scheduleService, finishRequested *atomic.Bool) {
	if server == nil || service == nil {
		return
	}
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        scheduleTaskToolName,
		Description: "Enqueue one PDCA child message. Provide session_id, target, and content. Valid targets are plan, do, check, and act. Child outcomes return to the root workflow by runtime policy.",
	}, service.ScheduleTask)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        finishToolName,
		Description: "Request runtime stop after the current root turn returns.",
	}, func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, map[string]string, error) {
		finishRequested.Store(true)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "finish requested"}},
		}, map[string]string{"status": "requested"}, nil
	})
}

func (s *scheduleService) ScheduleTask(_ context.Context, _ *sdkmcp.CallToolRequest, input scheduleTaskInput) (*sdkmcp.CallToolResult, scheduleTaskOutput, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	targetName, targetLocator, targetErr := normalizeTarget(input.Target)
	if s.controller == nil {
		out := scheduleTaskOutput{SessionID: sessionID, Target: targetName, Status: "error", Message: "controller is not ready"}
		return toolError(out.Message), out, nil
	}
	if targetErr != nil {
		out := scheduleTaskOutput{SessionID: sessionID, Status: "error", Message: targetErr.Error()}
		return toolError(out.Message), out, nil
	}
	if err := s.controller.Enqueue(taskmasterrt.Message{
		SessionID: sessionID,
		Kind:      taskmasterrt.MessageKindJob,
		From:      taskmasterrt.NewAgentLocator(rootAgentID),
		To:        targetLocator,
		Content:   input.Content,
	}); err != nil {
		out := scheduleTaskOutput{SessionID: sessionID, Target: targetName, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}
	out := scheduleTaskOutput{
		SessionID: sessionID,
		Target:    targetName,
		Status:    "queued",
		Message:   "message queued",
	}
	s.logger.Debug().
		Str("session_id", sessionID).
		Str("target", targetName).
		Msg("schedule_task accepted")
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: out.Message}},
	}, out, nil
}

func normalizeTarget(raw string) (string, taskmasterrt.Locator, error) {
	target := strings.ToLower(strings.TrimSpace(raw))
	switch target {
	case "plan", "do", "check", "act":
		return target, taskmasterrt.NewAgentLocator(target), nil
	default:
		return "", taskmasterrt.Locator{}, fmt.Errorf("unsupported target %q", raw)
	}
}

func toolError(message string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: message}},
	}
}
