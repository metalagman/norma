package handlers

import (
	"context"
	"testing"

	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/eventemitter"
	"github.com/tgbotkit/runtime/events"
	rtHandlers "github.com/tgbotkit/runtime/handlers"
	"github.com/tgbotkit/runtime/messagetype"
)

var _ rtHandlers.RegistryInterface = (*fakeRelayRegistry)(nil)

type fakeRelayRegistry struct {
	onMessageCalls   int
	messageTypeCalls []messagetype.MessageType
}

func (f *fakeRelayRegistry) OnUpdate(rtHandlers.UpdateHandler) eventemitter.UnsubscribeFunc {
	return func() {}
}

func (f *fakeRelayRegistry) OnMessage(rtHandlers.MessageHandler) eventemitter.UnsubscribeFunc {
	f.onMessageCalls++
	return func() {}
}

func (f *fakeRelayRegistry) OnMessageType(t messagetype.MessageType, _ rtHandlers.MessageHandler) eventemitter.UnsubscribeFunc {
	f.messageTypeCalls = append(f.messageTypeCalls, t)
	return func() {}
}

func (f *fakeRelayRegistry) OnCommand(rtHandlers.CommandHandler) eventemitter.UnsubscribeFunc {
	return func() {}
}

func TestRelayHandlerRegister_RegistersForumTopicMessageTypes(t *testing.T) {
	registry := &fakeRelayRegistry{}
	handler := &RelayHandler{logger: zerolog.Nop(), channel: newRelayTestTelegramAdapter()}

	handler.Register(registry)

	if registry.onMessageCalls != 1 {
		t.Fatalf("OnMessage calls = %d, want 1", registry.onMessageCalls)
	}

	want := []messagetype.MessageType{
		messagetype.ForumTopicCreated,
		messagetype.ForumTopicEdited,
		messagetype.ForumTopicClosed,
		messagetype.ForumTopicReopened,
	}
	if len(registry.messageTypeCalls) != len(want) {
		t.Fatalf("OnMessageType calls = %d, want %d", len(registry.messageTypeCalls), len(want))
	}
	for i := range want {
		if registry.messageTypeCalls[i] != want[i] {
			t.Fatalf("OnMessageType[%d] = %q, want %q", i, registry.messageTypeCalls[i], want[i])
		}
	}
}

func TestRelayHandlerOnForumTopicLifecycle_LogOnly(t *testing.T) {
	handler := &RelayHandler{logger: zerolog.Nop(), channel: newRelayTestTelegramAdapter()}

	tests := []messagetype.MessageType{
		messagetype.ForumTopicCreated,
		messagetype.ForumTopicEdited,
		messagetype.ForumTopicClosed,
		messagetype.ForumTopicReopened,
	}

	for _, messageType := range tests {
		t.Run(string(messageType), func(t *testing.T) {
			topicID := 77
			userID := int64(101)
			event := &events.MessageEvent{
				Type: messageType,
				Message: &client.Message{
					MessageId:       42,
					MessageThreadId: &topicID,
					Chat: client.Chat{
						Id:   9001,
						Type: "supergroup",
					},
					From: &client.User{Id: userID},
				},
			}

			if err := handler.onForumTopicLifecycle(context.Background(), event); err != nil {
				t.Fatalf("onForumTopicLifecycle() error = %v", err)
			}
		})
	}
}

func TestRelayHandlerOnForumTopicLifecycle_IgnoresOtherChatWhenBound(t *testing.T) {
	handler := &RelayHandler{logger: zerolog.Nop(), channel: newRelayTestTelegramAdapter()}
	handler.setChatID(9001)

	topicID := 13
	event := &events.MessageEvent{
		Type: messagetype.ForumTopicClosed,
		Message: &client.Message{
			MessageId:       55,
			MessageThreadId: &topicID,
			Chat: client.Chat{
				Id:   9999,
				Type: "supergroup",
			},
		},
	}

	if err := handler.onForumTopicLifecycle(context.Background(), event); err != nil {
		t.Fatalf("onForumTopicLifecycle() error = %v", err)
	}

	if got := handler.getChatID(); got != 9001 {
		t.Fatalf("chatID = %d, want 9001", got)
	}
}

func TestRelayHandlerOnForumTopicLifecycle_IgnoresEventWithoutTopicID(t *testing.T) {
	handler := &RelayHandler{logger: zerolog.Nop(), channel: newRelayTestTelegramAdapter()}

	event := &events.MessageEvent{
		Type: messagetype.ForumTopicClosed,
		Message: &client.Message{
			MessageId: 66,
			Chat: client.Chat{
				Id:   9001,
				Type: "supergroup",
			},
		},
	}

	if err := handler.onForumTopicLifecycle(context.Background(), event); err != nil {
		t.Fatalf("onForumTopicLifecycle() error = %v", err)
	}
}

func TestRelayHandlerOnMessage_IgnoresNilFrom(t *testing.T) {
	handler := &RelayHandler{logger: zerolog.Nop(), channel: newRelayTestTelegramAdapter()}
	handler.SetOwner(101, 9001)

	text := "hello"
	event := &events.MessageEvent{
		Type: messagetype.Text,
		Message: &client.Message{
			Chat: client.Chat{
				Id:   9001,
				Type: "private",
			},
			Text: &text,
			From: nil,
		},
	}

	if err := handler.onMessage(context.Background(), event); err != nil {
		t.Fatalf("onMessage() error = %v", err)
	}
}

func newRelayTestTelegramAdapter() *relaytelegram.Adapter {
	tgClient := &fakeTelegramClient{}
	msg := messenger.NewMessenger(tgClient, zerolog.Nop())
	return relaytelegram.NewAdapter(relaytelegram.AdapterParams{
		Messenger: msg,
		TGClient:  tgClient,
		Logger:    zerolog.Nop(),
	})
}
