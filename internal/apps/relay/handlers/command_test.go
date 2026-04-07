package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
)

func TestCommandHandlerOnCommand_CloseTopicAndStopSession(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	topicID := 123
	err := handler.onCommand(context.Background(), newCommandEvent("close", "", 101, 9001, &topicID))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(sm.closeCalls) != 1 {
		t.Fatalf("CloseTopic calls = %d, want 1", len(sm.closeCalls))
	}
	if len(sm.stopCalls) != 1 {
		t.Fatalf("StopSession calls = %d, want 1", len(sm.stopCalls))
	}
	if sm.closeCalls[0].chatID != 9001 || sm.closeCalls[0].topicID != topicID {
		t.Fatalf("CloseTopic call = %+v, want chat=9001 topic=%d", sm.closeCalls[0], topicID)
	}
	if sm.stopCalls[0].chatID != 9001 || sm.stopCalls[0].topicID != topicID {
		t.Fatalf("StopSession call = %+v, want chat=9001 topic=%d", sm.stopCalls[0], topicID)
	}
	assertLastSentContains(t, tgClient, "Closing this topic and stopping agent session.")
}

func TestCommandHandlerOnCommand_CloseRootStopsOnlySession(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	err := handler.onCommand(context.Background(), newCommandEvent("close", "", 101, 9001, nil))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(sm.closeCalls) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(sm.closeCalls))
	}
	if len(sm.stopCalls) != 1 {
		t.Fatalf("StopSession calls = %d, want 1", len(sm.stopCalls))
	}
	if sm.stopCalls[0].chatID != 9001 || sm.stopCalls[0].topicID != 0 {
		t.Fatalf("StopSession call = %+v, want chat=9001 topic=0", sm.stopCalls[0])
	}
	assertLastSentContains(t, tgClient, "Stopping root agent session.")
}

func TestCommandHandlerOnCommand_CloseWithArgsShowsUsage(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	topicID := 11
	err := handler.onCommand(context.Background(), newCommandEvent("close", "now", 101, 9001, &topicID))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(sm.closeCalls) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(sm.closeCalls))
	}
	if len(sm.stopCalls) != 0 {
		t.Fatalf("StopSession calls = %d, want 0", len(sm.stopCalls))
	}
	assertLastSentContains(t, tgClient, "Usage: /close")
}

func TestCommandHandlerOnCommand_CloseUnauthorized(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	topicID := 33
	err := handler.onCommand(context.Background(), newCommandEvent("close", "", 999, 9001, &topicID))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(sm.closeCalls) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(sm.closeCalls))
	}
	if len(sm.stopCalls) != 0 {
		t.Fatalf("StopSession calls = %d, want 0", len(sm.stopCalls))
	}
	assertLastSentContains(t, tgClient, "Only the bot owner can use this command.")
}

func TestCommandHandlerOnCommand_NewWithoutArgs_ShowsConfiguredAgentIDs(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	err := handler.onCommand(context.Background(), newCommandEvent("new", "", 101, 9001, nil))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(sm.closeCalls) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(sm.closeCalls))
	}
	if len(sm.stopCalls) != 0 {
		t.Fatalf("StopSession calls = %d, want 0", len(sm.stopCalls))
	}
	assertLastSentContains(t, tgClient, "Usage: /new <agent_id>")
	assertLastSentContains(t, tgClient, "Available agents: alpha, beta")
}

func TestCommandHandlerNewUsageMessage_NoAgentsConfigured(t *testing.T) {
	handler, _, _ := newCommandHandlerTestHarness(t)
	handler.agentIDs = nil

	got := handler.newCommandUsageMessage()
	if !strings.Contains(got, "No agents configured under norma.agents in relay config.") {
		t.Fatalf("newCommandUsageMessage() = %q, want no-agents guidance", got)
	}
}

type fakeCommandSessionManager struct {
	closeCalls []closeTopicCall
	stopCalls  []stopSessionCall
}

type closeTopicCall struct {
	chatID  int64
	topicID int
}

type stopSessionCall struct {
	chatID  int64
	topicID int
}

func (f *fakeCommandSessionManager) CreateTopicSession(context.Context, int64, string) (string, int, error) {
	return "", 0, nil
}

func (f *fakeCommandSessionManager) GetAgentInfo(string) (string, []string) {
	return "", nil
}

func (f *fakeCommandSessionManager) StopSession(chatID int64, topicID int) {
	f.stopCalls = append(f.stopCalls, stopSessionCall{chatID: chatID, topicID: topicID})
}

func (f *fakeCommandSessionManager) CloseTopic(_ context.Context, chatID int64, topicID int) {
	f.closeCalls = append(f.closeCalls, closeTopicCall{chatID: chatID, topicID: topicID})
}

func newCommandHandlerTestHarness(t *testing.T) (*CommandHandler, *fakeCommandSessionManager, *fakeTelegramClient) {
	t.Helper()

	stateStore := &fakeOwnerKVStore{}
	ownerStore, err := auth.NewOwnerStore(stateStore)
	if err != nil {
		t.Fatalf("NewOwnerStore(): %v", err)
	}
	_, err = ownerStore.RegisterOwner(101, 9001, "owner", "Owner", "", true)
	if err != nil {
		t.Fatalf("RegisterOwner(): %v", err)
	}

	tgClient := &fakeTelegramClient{}
	msg := messenger.NewMessenger(tgClient, zerolog.Nop())
	sessionManager := &fakeCommandSessionManager{}
	handler := &CommandHandler{
		ownerStore:     ownerStore,
		sessionManager: sessionManager,
		messenger:      msg,
		agentIDs:       []string{"alpha", "beta"},
	}
	return handler, sessionManager, tgClient
}

func newCommandEvent(command, args string, userID, chatID int64, topicID *int) *events.CommandEvent {
	text := "/" + command
	if trimmedArgs := strings.TrimSpace(args); trimmedArgs != "" {
		text += " " + trimmedArgs
	}
	msg := &client.Message{
		Chat: client.Chat{
			Id:   chatID,
			Type: "private",
		},
		From: &client.User{
			Id:        userID,
			FirstName: "Test",
		},
		Text: &text,
	}
	if topicID != nil {
		msg.MessageThreadId = topicID
	}
	return &events.CommandEvent{
		Command: command,
		Args:    args,
		Message: msg,
	}
}
