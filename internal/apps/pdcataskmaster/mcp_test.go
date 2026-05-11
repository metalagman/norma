package pdcataskmaster

import (
	"context"
	"reflect"
	"testing"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

func TestScheduleTaskMapsFlatTarget(t *testing.T) {
	t.Parallel()

	controller := &fakeScheduleController{}
	service := newScheduleService(zerolog.Nop())
	service.SetController(controller)

	_, out, err := service.ScheduleTask(context.Background(), nil, scheduleTaskInput{
		SessionID: "session-a",
		Target:    "plan",
		Content:   "ship the goal",
	})
	if err != nil {
		t.Fatalf("ScheduleTask() error = %v", err)
	}
	if out.Status != "queued" {
		t.Fatalf("status = %q, want queued", out.Status)
	}
	if controller.lastMessage.Kind != taskmasterrt.MessageKindJob {
		t.Fatalf("kind = %q, want job", controller.lastMessage.Kind)
	}
	if !reflect.DeepEqual(controller.lastMessage.From, taskmasterrt.NewAgentLocator(rootAgentID)) {
		t.Fatalf("from = %+v, want root locator", controller.lastMessage.From)
	}
	if !reflect.DeepEqual(controller.lastMessage.To, taskmasterrt.NewAgentLocator("plan")) {
		t.Fatalf("target locator = %+v, want plan", controller.lastMessage.To)
	}
	if controller.lastMessage.Content != "ship the goal" {
		t.Fatalf("content = %q, want raw content", controller.lastMessage.Content)
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
	lastMessage taskmasterrt.Message
}

func (c *fakeScheduleController) Enqueue(msg taskmasterrt.Message) error {
	c.lastMessage = msg
	return nil
}
