package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
)

type fakeOwnerKVStore struct {
	value any
	ok    bool
	err   error
}

func (s *fakeOwnerKVStore) GetJSON(_ context.Context, _ string) (any, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	return s.value, s.ok, nil
}

func (s *fakeOwnerKVStore) SetJSON(_ context.Context, _ string, value any) error {
	if s.err != nil {
		return s.err
	}
	s.value = value
	s.ok = true
	return nil
}

type fakeTelegramClient struct {
	client.ClientWithResponsesInterface
	sendErr  error
	messages []client.SendMessageJSONRequestBody
}

func (c *fakeTelegramClient) SendMessageWithResponse(_ context.Context, body client.SendMessageJSONRequestBody, _ ...client.RequestEditorFn) (*client.SendMessageResponse, error) {
	c.messages = append(c.messages, body)
	if c.sendErr != nil {
		return nil, c.sendErr
	}
	return &client.SendMessageResponse{}, nil
}

type fakeRelayOwnerActivator struct {
	calls []activationCall
	err   error
}

type activationCall struct {
	ownerID int64
	chatID  int64
}

func (f *fakeRelayOwnerActivator) ActivateOwner(_ context.Context, ownerID, chatID int64) error {
	f.calls = append(f.calls, activationCall{ownerID: ownerID, chatID: chatID})
	return f.err
}

func TestParseStartAuthArg(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantToken     string
		wantMalformed bool
	}{
		{
			name:      "empty args",
			raw:       "   ",
			wantToken: "",
		},
		{
			name:      "plain token",
			raw:       "abc123",
			wantToken: "abc123",
		},
		{
			name:          "query-like token with question mark",
			raw:           "?abc123",
			wantMalformed: true,
		},
		{
			name:          "query-like start assignment",
			raw:           "start=abc123",
			wantMalformed: true,
		},
		{
			name:          "token with equals rejected in strict mode",
			raw:           "abc=123",
			wantMalformed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotToken, gotMalformed := parseStartAuthArg(tc.raw)
			if gotToken != tc.wantToken {
				t.Fatalf("token = %q, want %q", gotToken, tc.wantToken)
			}
			if gotMalformed != tc.wantMalformed {
				t.Fatalf("malformed = %t, want %t", gotMalformed, tc.wantMalformed)
			}
		})
	}
}

func TestStartHandlerOnCommand_StrictAuthFlow(t *testing.T) {
	t.Run("accepts slash-start token", func(t *testing.T) {
		handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")
		relay := &fakeRelayOwnerActivator{}
		handler.SetRelayHandler(relay)

		err := handler.onCommand(context.Background(), newStartEvent("secret-token", 101, 9001))
		if err != nil {
			t.Fatalf("onCommand(): %v", err)
		}

		if !store.HasOwner() {
			t.Fatal("owner not registered")
		}
		owner := store.GetOwner()
		if owner == nil {
			t.Fatal("owner is nil")
		}
		if owner.UserID != 101 {
			t.Fatalf("owner.UserID = %d, want 101", owner.UserID)
		}
		if len(relay.calls) != 1 {
			t.Fatalf("ActivateOwner calls = %d, want 1", len(relay.calls))
		}
		if got := relay.calls[0]; got.ownerID != 101 || got.chatID != 9001 {
			t.Fatalf("ActivateOwner call = %+v, want owner=101 chat=9001", got)
		}
		assertLastSentContains(t, tgClient, "Congratulations")
	})

	t.Run("rejects malformed question-mark token", func(t *testing.T) {
		handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")

		err := handler.onCommand(context.Background(), newStartEvent("?secret-token", 101, 9001))
		if err != nil {
			t.Fatalf("onCommand(): %v", err)
		}

		if store.HasOwner() {
			t.Fatal("owner registered unexpectedly")
		}
		assertLastSentContains(t, tgClient, "Invalid /start format")
		assertLastSentContains(t, tgClient, "https://t.me/<bot_username>?start=<your_owner_token>")
	})

	t.Run("rejects malformed start assignment", func(t *testing.T) {
		handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")

		err := handler.onCommand(context.Background(), newStartEvent("start=secret-token", 101, 9001))
		if err != nil {
			t.Fatalf("onCommand(): %v", err)
		}

		if store.HasOwner() {
			t.Fatal("owner registered unexpectedly")
		}
		assertLastSentContains(t, tgClient, "Invalid /start format")
	})

	t.Run("keeps welcome flow for empty args", func(t *testing.T) {
		handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")

		err := handler.onCommand(context.Background(), newStartEvent("   ", 101, 9001))
		if err != nil {
			t.Fatalf("onCommand(): %v", err)
		}

		if store.HasOwner() {
			t.Fatal("owner registered unexpectedly")
		}
		assertLastSentContains(t, tgClient, "To authenticate, send /start <your_owner_token>")
	})
}

func TestStartHandlerOnCommand_ExistingOwner_StartsRootWhenMissing(t *testing.T) {
	handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")
	relay := &fakeRelayOwnerActivator{}
	handler.SetRelayHandler(relay)

	registered, err := store.RegisterOwner(101, 0, "owner", "Owner", "", true)
	if err != nil {
		t.Fatalf("RegisterOwner(): %v", err)
	}
	if !registered {
		t.Fatal("owner should be newly registered")
	}

	err = handler.onCommand(context.Background(), newStartEvent("", 101, 9001))
	if err != nil {
		t.Fatalf("onCommand(): %v", err)
	}

	if len(relay.calls) != 1 {
		t.Fatalf("ActivateOwner calls = %d, want 1", len(relay.calls))
	}
	assertLastSentContains(t, tgClient, "You are already registered as the bot owner. Relay mode is active.")
}

func TestStartHandlerOnCommand_RelayActivationFailure_DoesNotBlockOwnerRegistration(t *testing.T) {
	handler, store, tgClient := newStartHandlerTestHarness(t, "secret-token")
	relay := &fakeRelayOwnerActivator{err: errors.New("precreate failed")}
	handler.SetRelayHandler(relay)

	err := handler.onCommand(context.Background(), newStartEvent("secret-token", 101, 9001))
	if err != nil {
		t.Fatalf("onCommand(): %v", err)
	}

	if !store.HasOwner() {
		t.Fatal("owner not registered")
	}
	assertLastSentContains(t, tgClient, "Congratulations")
	assertLastSentContains(t, tgClient, "Failed to start root agent session: precreate failed.")
}

func TestStartHandlerOnCommand_SendErrorBubblesUp(t *testing.T) {
	handler, _, tgClient := newStartHandlerTestHarness(t, "secret-token")
	tgClient.sendErr = errors.New("send failed")

	err := handler.onCommand(context.Background(), newStartEvent("   ", 101, 9001))
	if err == nil {
		t.Fatal("onCommand() error = nil, want send error")
	}
}

func newStartHandlerTestHarness(t *testing.T, authToken string) (*StartHandler, *auth.OwnerStore, *fakeTelegramClient) {
	t.Helper()

	stateStore := &fakeOwnerKVStore{}
	ownerStore, err := auth.NewOwnerStore(stateStore)
	if err != nil {
		t.Fatalf("NewOwnerStore(): %v", err)
	}

	tgClient := &fakeTelegramClient{}
	msg := messenger.NewMessenger(tgClient, zerolog.Nop())
	handler := NewStartHandler(StartHandlerParams{
		OwnerStore: ownerStore,
		Messenger:  msg,
		AuthToken:  authToken,
	})

	return handler, ownerStore, tgClient
}

func newStartEvent(args string, userID, chatID int64) *events.CommandEvent {
	text := "/start " + strings.TrimSpace(args)
	return &events.CommandEvent{
		Command: "start",
		Args:    args,
		Message: &client.Message{
			Chat: client.Chat{
				Id:   chatID,
				Type: "private",
			},
			From: &client.User{
				Id:        userID,
				FirstName: "Test",
			},
			Text: &text,
		},
	}
}

func assertLastSentContains(t *testing.T, tgClient *fakeTelegramClient, wantSubstring string) {
	t.Helper()
	if len(tgClient.messages) == 0 {
		t.Fatal("no messages were sent")
	}
	last := tgClient.messages[len(tgClient.messages)-1]
	if !strings.Contains(last.Text, wantSubstring) {
		t.Fatalf("last message = %q, want substring %q", last.Text, wantSubstring)
	}
}
