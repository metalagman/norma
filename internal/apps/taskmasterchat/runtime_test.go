package taskmasterchat

import (
	"context"
	"reflect"
	"strings"
	"testing"

	taskmasterrt "github.com/normahq/runtime/v2/taskmaster"
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

func TestBuildChatMessage(t *testing.T) {
	t.Parallel()

	if got := buildChatMessage(""); got != nil {
		t.Fatalf("buildChatMessage(empty) = %+v, want nil", got)
	}

	got := buildChatMessage("  hello world  ")
	if got == nil {
		t.Fatal("buildChatMessage(non-empty) = nil, want message")
		return
	}
	if got.SessionID != fakeChatSessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, fakeChatSessionID)
	}
	if got.Kind != taskmasterrt.MessageKindJob {
		t.Fatalf("Kind = %q, want job", got.Kind)
	}
	if !reflect.DeepEqual(got.From, taskmasterrt.NewFakeChatHumanLocator(fakeChatID)) {
		t.Fatalf("From = %+v, want fake chat locator", got.From)
	}
	if !reflect.DeepEqual(got.To, taskmasterrt.NewAgentLocator(taskmasterAgentID)) {
		t.Fatalf("To = %+v, want root agent locator", got.To)
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
	if len(runtime.messages) != 2 {
		t.Fatalf("enqueued messages = %d, want 2", len(runtime.messages))
	}
	if runtime.messages[0].Content != "hello" {
		t.Fatalf("first content = %q, want hello", runtime.messages[0].Content)
	}
	if runtime.messages[1].Content != "   " {
		t.Fatalf("second content = %q, want raw spaces", runtime.messages[1].Content)
	}
	for _, msg := range runtime.messages {
		if msg.SessionID != fakeChatSessionID {
			t.Fatalf("SessionID = %q, want %q", msg.SessionID, fakeChatSessionID)
		}
		if !reflect.DeepEqual(msg.From, taskmasterrt.NewFakeChatHumanLocator(fakeChatID)) {
			t.Fatalf("From = %+v, want fake chat locator", msg.From)
		}
		if !reflect.DeepEqual(msg.To, taskmasterrt.NewAgentLocator(taskmasterAgentID)) {
			t.Fatalf("To = %+v, want root agent locator", msg.To)
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
	if err := target.DispatchMessage(context.Background(), taskmasterrt.Message{
		SessionID: fakeChatSessionID,
		Kind:      taskmasterrt.MessageKindNotification,
		From:      taskmasterrt.NewAgentLocator(taskmasterAgentID),
		To:        locator,
		Content:   "done",
	}); err != nil {
		t.Fatalf("DispatchMessage() error = %v", err)
	}
	if got := writer.String(); got != "taskmaster> done\nyou> " {
		t.Fatalf("output = %q, want fake chat reply format", got)
	}
}

type fakeEnqueuer struct {
	messages []taskmasterrt.Message
}

func (f *fakeEnqueuer) Enqueue(msg taskmasterrt.Message) error {
	f.messages = append(f.messages, msg)
	return nil
}
