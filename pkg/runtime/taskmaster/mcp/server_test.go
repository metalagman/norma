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
	service := NewService(zerolog.Nop(), taskmaster.NewAgentLocator("taskmaster"), true)
	service.SetController(controller)

	_, out, err := service.scheduleTask(context.Background(), nil, ScheduleTaskInput{
		TaskID:    "task-1",
		SessionID: "session-a",
		Locator:   taskmaster.NewAgentLocator("worker"),
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("scheduleTask() error = %v", err)
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

func TestFinishCallsController(t *testing.T) {
	t.Parallel()

	controller := &fakeController{}
	service := NewService(zerolog.Nop(), taskmaster.NewAgentLocator("taskmaster"), true)
	service.SetController(controller)

	_, out, err := service.finish(context.Background(), nil, FinishInput{Summary: "done"})
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if out.Status != "finished" {
		t.Fatalf("status = %q, want finished", out.Status)
	}
	if controller.finishSummary != "done" {
		t.Fatalf("finishSummary = %q, want done", controller.finishSummary)
	}
}

func TestToolNamesStayStable(t *testing.T) {
	t.Parallel()

	if ScheduleTaskToolName != "taskmaster.schedule_task" {
		t.Fatalf("ScheduleTaskToolName = %q", ScheduleTaskToolName)
	}
	if FinishToolName != "taskmaster.finish" {
		t.Fatalf("FinishToolName = %q", FinishToolName)
	}
}

type fakeController struct {
	lastTask      taskmaster.Task
	finishSummary string
}

func (c *fakeController) Enqueue(task taskmaster.Task) error {
	c.lastTask = task
	return nil
}

func (c *fakeController) Finish(summary string) error {
	c.finishSummary = summary
	return nil
}
