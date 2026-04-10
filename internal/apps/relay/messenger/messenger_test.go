package messenger

import (
	"context"
	"net/http"
	"testing"

	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
)

type fakeChatActionClient struct {
	client.ClientWithResponsesInterface
	chatActions []client.SendChatActionJSONRequestBody
}

func (f *fakeChatActionClient) SendChatActionWithResponse(
	_ context.Context,
	body client.SendChatActionJSONRequestBody,
	_ ...client.RequestEditorFn,
) (*client.SendChatActionResponse, error) {
	f.chatActions = append(f.chatActions, body)
	return &client.SendChatActionResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
		JSON200: &struct {
			Ok     client.SendChatAction200Ok `json:"ok"`
			Result bool                       `json:"result"`
		}{
			Ok:     true,
			Result: true,
		},
	}, nil
}

func TestSendChatAction_IncludesMessageThreadIDWhenTopicProvided(t *testing.T) {
	t.Parallel()

	tgClient := &fakeChatActionClient{}
	m := NewMessenger(tgClient, zerolog.Nop())

	if err := m.SendChatAction(context.Background(), 9001, 77, "typing"); err != nil {
		t.Fatalf("SendChatAction() error = %v", err)
	}

	if len(tgClient.chatActions) != 1 {
		t.Fatalf("chatActions calls = %d, want 1", len(tgClient.chatActions))
	}
	got := tgClient.chatActions[0]
	if got.ChatId != 9001 {
		t.Fatalf("chat_id = %d, want 9001", got.ChatId)
	}
	if got.Action != "typing" {
		t.Fatalf("action = %q, want typing", got.Action)
	}
	if got.MessageThreadId == nil || *got.MessageThreadId != 77 {
		t.Fatalf("message_thread_id = %v, want 77", got.MessageThreadId)
	}
}

func TestSendChatAction_OmitsMessageThreadIDForRootChat(t *testing.T) {
	t.Parallel()

	tgClient := &fakeChatActionClient{}
	m := NewMessenger(tgClient, zerolog.Nop())

	if err := m.SendChatAction(context.Background(), 9001, 0, "typing"); err != nil {
		t.Fatalf("SendChatAction() error = %v", err)
	}

	if len(tgClient.chatActions) != 1 {
		t.Fatalf("chatActions calls = %d, want 1", len(tgClient.chatActions))
	}
	if tgClient.chatActions[0].MessageThreadId != nil {
		t.Fatalf("message_thread_id = %v, want nil", tgClient.chatActions[0].MessageThreadId)
	}
}
