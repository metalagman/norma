package mcp

import (
	"context"
	"testing"

	"github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

func TestScheduleTaskUsesContentAndDefaultReportTo(t *testing.T) {
	t.Parallel()

	controller := &fakeController{}
	service := NewService(zerolog.Nop(), taskmaster.NewAgentLocator("taskmaster"))
	service.SetController(controller)

	_, out, err := service.ScheduleTask(context.Background(), nil, ScheduleTaskInput{
		TaskID:    "task-1",
		SessionID: "session-a",
		Locator:   taskmaster.NewAgentLocator("worker"),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("ScheduleTask() error = %v", err)
	}
	if out.Status != "queued" {
		t.Fatalf("status = %q, want queued", out.Status)
	}
	if controller.lastTask.Content != "do work" {
		t.Fatalf("content = %q, want do work", controller.lastTask.Content)
	}
	if controller.lastTask.ReportTo == nil || controller.lastTask.ReportTo.Key != "taskmaster" {
		t.Fatalf("report_to = %+v, want default root report_to", controller.lastTask.ReportTo)
	}
}

func TestToolNameStaysStable(t *testing.T) {
	t.Parallel()

	if ScheduleTaskToolName != "taskmaster.schedule_task" {
		t.Fatalf("ScheduleTaskToolName = %q", ScheduleTaskToolName)
	}
}

type fakeController struct {
	lastTask taskmaster.Task
}

func (c *fakeController) Enqueue(task taskmaster.Task) error {
	c.lastTask = task
	return nil
}
