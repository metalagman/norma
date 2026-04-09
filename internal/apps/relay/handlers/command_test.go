package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/normahq/norma/internal/apps/relay/session"
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

	if len(tgClient.closedTopicIDs) != 1 {
		t.Fatalf("CloseTopic calls = %d, want 1", len(tgClient.closedTopicIDs))
	}
	if len(sm.stopCalls) != 1 {
		t.Fatalf("StopSession calls = %d, want 1", len(sm.stopCalls))
	}
	if tgClient.closedTopicIDs[0] != topicID {
		t.Fatalf("CloseTopic call = %d, want topic=%d", tgClient.closedTopicIDs[0], topicID)
	}
	if sm.stopCalls[0].SessionID != "tg-9001-123" {
		t.Fatalf("StopSession call = %+v, want session=tg-9001-123", sm.stopCalls[0])
	}
	assertLastSentContains(t, tgClient, "Closing this topic and stopping agent session.")
}

func TestCommandHandlerOnCommand_CloseRootStopsOnlySession(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)

	err := handler.onCommand(context.Background(), newCommandEvent("close", "", 101, 9001, nil))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(tgClient.closedTopicIDs) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(tgClient.closedTopicIDs))
	}
	if len(sm.stopCalls) != 1 {
		t.Fatalf("StopSession calls = %d, want 1", len(sm.stopCalls))
	}
	if sm.stopCalls[0].SessionID != "tg-9001-0" {
		t.Fatalf("StopSession call = %+v, want session=tg-9001-0", sm.stopCalls[0])
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

	if len(tgClient.closedTopicIDs) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(tgClient.closedTopicIDs))
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

	if len(tgClient.closedTopicIDs) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(tgClient.closedTopicIDs))
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

	if len(tgClient.closedTopicIDs) != 0 {
		t.Fatalf("CloseTopic calls = %d, want 0", len(tgClient.closedTopicIDs))
	}
	if len(sm.stopCalls) != 0 {
		t.Fatalf("StopSession calls = %d, want 0", len(sm.stopCalls))
	}
	assertLastSentContains(t, tgClient, "Usage: /new <agent_id>")
	assertLastSentContains(t, tgClient, "Available agents: alpha, beta")
}

func TestCommandHandlerOnCommand_NewCreatesTopicSession(t *testing.T) {
	handler, sm, tgClient := newCommandHandlerTestHarness(t)
	tgClient.nextTopicID = 456

	err := handler.onCommand(context.Background(), newCommandEvent("new", "alpha", 101, 9001, nil))
	if err != nil {
		t.Fatalf("onCommand() error = %v", err)
	}

	if len(tgClient.createdTopics) != 1 {
		t.Fatalf("CreateTopic calls = %d, want 1", len(tgClient.createdTopics))
	}
	if tgClient.createdTopics[0].Name != "Relay: alpha" {
		t.Fatalf("CreateTopic name = %q, want %q", tgClient.createdTopics[0].Name, "Relay: alpha")
	}
	if len(sm.createCalls) != 1 {
		t.Fatalf("CreateSession calls = %d, want 1", len(sm.createCalls))
	}
	if sm.createCalls[0].SessionID != "tg-9001-456" || sm.createCalls[0].AgentName != "alpha" {
		t.Fatalf("CreateSession call = %+v, want session=tg-9001-456 agent=alpha", sm.createCalls[0])
	}
	assertLastSentContains(t, tgClient, "tg\\-9001\\-456")
	assertLastSentContains(t, tgClient, "***alpha***")
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
	stopCalls   []stopSessionCall
	createCalls []createSessionCall
}

type createSessionCall struct {
	SessionID string
	AgentName string
}

type stopSessionCall struct {
	SessionID string
}

func (f *fakeCommandSessionManager) CreateSession(_ context.Context, locator session.SessionLocator, agentName string) error {
	f.createCalls = append(f.createCalls, createSessionCall{SessionID: locator.SessionID, AgentName: agentName})
	return nil
}

func (f *fakeCommandSessionManager) GetAgentInfo(string) (string, []string) {
	return "", nil
}

func (f *fakeCommandSessionManager) StopSession(locator session.SessionLocator) {
	f.stopCalls = append(f.stopCalls, stopSessionCall{SessionID: locator.SessionID})
}

func (f *fakeCommandSessionManager) ValidateAgent(string) error {
	return nil
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
		ownerStore: ownerStore,
		channel: relaytelegram.NewAdapter(relaytelegram.AdapterParams{
			Messenger: msg,
			TGClient:  tgClient,
			Logger:    zerolog.Nop(),
		}),
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
