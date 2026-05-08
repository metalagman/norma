package taskmasterchat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
)

func TestRootInstructionDefinesFakeChatCoordinator(t *testing.T) {
	t.Parallel()

	got := rootInstruction()
	for _, want := range []string{
		"generic Taskmaster inbox agent",
		"local fake-chat conversation turns",
		"plain-text result back to the fake chat outbox",
		"Do not impose a fixed workflow or phase order",
		"Return only the useful result as plain text",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"strict PDCA",
		"taskmaster.schedule_task",
		"taskmaster.finish",
		"worker",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestBuildChatTask(t *testing.T) {
	t.Parallel()

	if got := buildChatTask(""); got != nil {
		t.Fatalf("buildChatTask(empty) = %+v, want nil", got)
	}

	got := buildChatTask("  hello world  ")
	if got == nil {
		t.Fatal("buildChatTask(non-empty) = nil, want task")
	}
	if got.SessionID != fakeChatSessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, fakeChatSessionID)
	}
	if !reflect.DeepEqual(got.Locator, taskmasterrt.NewAgentLocator(taskmasterAgentID)) {
		t.Fatalf("Locator = %+v, want root agent locator", got.Locator)
	}
	if got.ReportTo == nil || !reflect.DeepEqual(*got.ReportTo, taskmasterrt.NewFakeChatHumanLocator(fakeChatID)) {
		t.Fatalf("ReportTo = %+v, want fake chat locator", got.ReportTo)
	}
	if got.Content != "  hello world  " {
		t.Fatalf("Content = %q, want raw content", got.Content)
	}
}

func TestEnqueueChatInputsUsesSingleSessionAndIgnoresEmptyLines(t *testing.T) {
	t.Parallel()

	runtime := &fakeEnqueuer{}
	console := &chatConsole{writer: &strings.Builder{}}

	err := enqueueChatInputs(context.Background(), strings.NewReader("hello\n\n   \n/quit\n"), console, runtime)
	if err != nil {
		t.Fatalf("enqueueChatInputs() error = %v", err)
	}
	if len(runtime.tasks) != 2 {
		t.Fatalf("enqueued tasks = %d, want 2", len(runtime.tasks))
	}
	if runtime.tasks[0].Content != "hello" {
		t.Fatalf("first content = %q, want hello", runtime.tasks[0].Content)
	}
	if runtime.tasks[1].Content != "   " {
		t.Fatalf("second content = %q, want raw spaces", runtime.tasks[1].Content)
	}
	for _, task := range runtime.tasks {
		if task.SessionID != fakeChatSessionID {
			t.Fatalf("SessionID = %q, want %q", task.SessionID, fakeChatSessionID)
		}
		if task.ReportTo == nil || !reflect.DeepEqual(*task.ReportTo, taskmasterrt.NewFakeChatHumanLocator(fakeChatID)) {
			t.Fatalf("ReportTo = %+v, want fake chat locator", task.ReportTo)
		}
	}
}

func TestFakeChatTargetDispatchesFormattedReply(t *testing.T) {
	t.Parallel()

	writer := &strings.Builder{}
	console := &chatConsole{writer: writer}
	target := newFakeChatTarget(console)
	locator := taskmasterrt.NewFakeChatHumanLocator(fakeChatID)
	if !target.Supports(locator) {
		t.Fatal("fake chat target does not support fake chat locator")
	}
	if target.Supports(taskmasterrt.NewTelegramHumanLocator(1, 2)) {
		t.Fatal("fake chat target supports telegram locator, want fake chat only")
	}
	if err := target.DispatchTask(context.Background(), taskmasterrt.Task{
		SessionID: fakeChatSessionID,
		Locator:   locator,
		Content:   "done",
	}); err != nil {
		t.Fatalf("DispatchTask() error = %v", err)
	}
	if got := writer.String(); got != "taskmaster> done\nyou> " {
		t.Fatalf("output = %q, want fake chat reply format", got)
	}
}

type fakeEnqueuer struct {
	tasks []taskmasterrt.Task
}

func (f *fakeEnqueuer) Enqueue(task taskmasterrt.Task) error {
	f.tasks = append(f.tasks, task)
	return nil
}
