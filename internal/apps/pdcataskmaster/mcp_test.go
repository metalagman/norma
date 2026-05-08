package pdcataskmaster

import (
	"context"
	"reflect"
	"testing"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

func TestScheduleTaskMapsFlatTargetAndReportTo(t *testing.T) {
	t.Parallel()

	controller := &fakeScheduleController{}
	service := newScheduleService(zerolog.Nop())
	service.SetController(controller)

	_, out, err := service.ScheduleTask(context.Background(), nil, scheduleTaskInput{
		SessionID: "session-a",
		Target:    "plan",
		ReportTo:  "root",
		Content:   "ship the goal",
	})
	if err != nil {
		t.Fatalf("ScheduleTask() error = %v", err)
	}
	if out.Status != "queued" {
		t.Fatalf("status = %q, want queued", out.Status)
	}
	if !reflect.DeepEqual(controller.lastTask.Locator, taskmasterrt.NewAgentLocator("plan")) {
		t.Fatalf("target locator = %+v, want plan", controller.lastTask.Locator)
	}
	if controller.lastTask.ReportTo == nil || !reflect.DeepEqual(*controller.lastTask.ReportTo, taskmasterrt.NewAgentLocator(rootAgentID)) {
		t.Fatalf("report_to = %+v, want root locator", controller.lastTask.ReportTo)
	}
	if controller.lastTask.Content != "ship the goal" {
		t.Fatalf("content = %q, want raw content", controller.lastTask.Content)
	}
}

func TestScheduleTaskRejectsUnsupportedTarget(t *testing.T) {
	t.Parallel()

	service := newScheduleService(zerolog.Nop())
	service.SetController(&fakeScheduleController{})

	result, out, err := service.ScheduleTask(context.Background(), nil, scheduleTaskInput{
		SessionID: "session-a",
		Target:    "worker",
		Content:   "do work",
	})
	if err != nil {
		t.Fatalf("ScheduleTask() error = %v", err)
	}
	if !result.IsError || out.Status != "error" {
		t.Fatalf("ScheduleTask() result = %+v, out = %+v, want tool error", result, out)
	}
}

type fakeScheduleController struct {
	lastTask taskmasterrt.Task
}

func (c *fakeScheduleController) Enqueue(task taskmasterrt.Task) error {
	c.lastTask = task
	return nil
}
