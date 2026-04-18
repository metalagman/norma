package telegram

import (
	"testing"

	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/messagetype"
)

const testMessageText = "hello"

func TestMessageContextFromEvent_PrivateChatIgnoresMessageThreadID(t *testing.T) {
	topicID := 523431
	isTopicMessage := false

	got, ok := (&Adapter{}).MessageContextFromEvent(&events.MessageEvent{
		Message: &client.Message{
			MessageId:       11,
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
			Text: textPtr(testMessageText),
		},
	})
	if !ok {
		t.Fatal("MessageContextFromEvent() ok = false, want true")
	}
	if got.TopicID != 0 {
		t.Fatalf("topic_id = %d, want 0 for private chat", got.TopicID)
	}
	if !got.AllowProgressHints {
		t.Fatalf("allow_progress_hints = %v, want true for private chat", got.AllowProgressHints)
	}
	if got.Locator.SessionID != "tg-2317500-0" {
		t.Fatalf("session_id = %q, want tg-2317500-0", got.Locator.SessionID)
	}
}

func TestMessageContextFromEvent_SupergroupPreservesMessageThreadID(t *testing.T) {
	topicID := 77

	got, ok := (&Adapter{}).MessageContextFromEvent(&events.MessageEvent{
		Message: &client.Message{
			MessageId:       21,
			MessageThreadId: &topicID,
			Chat: client.Chat{
				Id:   -1009001,
				Type: "supergroup",
			},
			From: &client.User{Id: 101},
			Text: textPtr(testMessageText),
		},
	})
	if !ok {
		t.Fatal("MessageContextFromEvent() ok = false, want true")
	}
	if got.TopicID != 77 {
		t.Fatalf("topic_id = %d, want 77 for supergroup topic", got.TopicID)
	}
	if got.AllowProgressHints {
		t.Fatalf("allow_progress_hints = %v, want false for supergroup chat", got.AllowProgressHints)
	}
	if got.Locator.SessionID != "tg--1009001-77" {
		t.Fatalf("session_id = %q, want tg--1009001-77", got.Locator.SessionID)
	}
}

func TestMessageContextFromEvent_PrivateTopicPreservesMessageThreadID(t *testing.T) {
	topicID := 523431
	isTopicMessage := true

	got, ok := (&Adapter{}).MessageContextFromEvent(&events.MessageEvent{
		Message: &client.Message{
			MessageId:       31,
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
			Text: textPtr(testMessageText),
		},
	})
	if !ok {
		t.Fatal("MessageContextFromEvent() ok = false, want true")
	}
	if got.TopicID != 523431 {
		t.Fatalf("topic_id = %d, want 523431 for private topic message", got.TopicID)
	}
	if !got.AllowProgressHints {
		t.Fatalf("allow_progress_hints = %v, want true for private topic", got.AllowProgressHints)
	}
	if got.Locator.SessionID != "tg-2317500-523431" {
		t.Fatalf("session_id = %q, want tg-2317500-523431", got.Locator.SessionID)
	}
}

func TestMessageContextFromEvent_PopulatesReplyMetadataWhenPresent(t *testing.T) {
	got, ok := (&Adapter{}).MessageContextFromEvent(&events.MessageEvent{
		Message: &client.Message{
			MessageId: 41,
			Chat: client.Chat{
				Id:   -1009001,
				Type: "supergroup",
			},
			From: &client.User{Id: 101},
			Text: textPtr(testMessageText),
			ReplyToMessage: &client.Message{
				MessageId: 7,
				From: &client.User{
					Id:    404,
					IsBot: true,
				},
			},
		},
	})
	if !ok {
		t.Fatal("MessageContextFromEvent() ok = false, want true")
	}
	if !got.IsReply {
		t.Fatalf("is_reply = %v, want true", got.IsReply)
	}
	if got.ReplyToUserID != 404 {
		t.Fatalf("reply_to_user_id = %d, want 404", got.ReplyToUserID)
	}
	if !got.ReplyToIsBot {
		t.Fatalf("reply_to_is_bot = %v, want true", got.ReplyToIsBot)
	}
}

func TestMessageContextFromEvent_ReplyMetadataEmptyWithoutReply(t *testing.T) {
	got, ok := (&Adapter{}).MessageContextFromEvent(&events.MessageEvent{
		Message: &client.Message{
			MessageId: 42,
			Chat: client.Chat{
				Id:   -1009001,
				Type: "supergroup",
			},
			From: &client.User{Id: 101},
			Text: textPtr(testMessageText),
		},
	})
	if !ok {
		t.Fatal("MessageContextFromEvent() ok = false, want true")
	}
	if got.IsReply {
		t.Fatalf("is_reply = %v, want false", got.IsReply)
	}
	if got.ReplyToUserID != 0 {
		t.Fatalf("reply_to_user_id = %d, want 0", got.ReplyToUserID)
	}
	if got.ReplyToIsBot {
		t.Fatalf("reply_to_is_bot = %v, want false", got.ReplyToIsBot)
	}
}

func TestCommandContextFromEvent_PrivateChatIgnoresMessageThreadID(t *testing.T) {
	topicID := 523431
	isTopicMessage := false

	got, ok := (&Adapter{}).CommandContextFromEvent(&events.CommandEvent{
		Command: "new",
		Args:    "codex",
		Message: &client.Message{
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
		},
	})
	if !ok {
		t.Fatal("CommandContextFromEvent() ok = false, want true")
	}
	if got.TopicID != 0 {
		t.Fatalf("topic_id = %d, want 0 for private chat", got.TopicID)
	}
	if got.Locator.SessionID != "tg-2317500-0" {
		t.Fatalf("session_id = %q, want tg-2317500-0", got.Locator.SessionID)
	}
}

func TestCommandContextFromEvent_PrivateTopicPreservesMessageThreadID(t *testing.T) {
	topicID := 523431
	isTopicMessage := true

	got, ok := (&Adapter{}).CommandContextFromEvent(&events.CommandEvent{
		Command: "new",
		Args:    "codex",
		Message: &client.Message{
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
		},
	})
	if !ok {
		t.Fatal("CommandContextFromEvent() ok = false, want true")
	}
	if got.TopicID != 523431 {
		t.Fatalf("topic_id = %d, want 523431 for private topic command", got.TopicID)
	}
	if got.Locator.SessionID != "tg-2317500-523431" {
		t.Fatalf("session_id = %q, want tg-2317500-523431", got.Locator.SessionID)
	}
}

func TestTopicLifecycleFromEvent_IgnoresPrivateNonTopicChat(t *testing.T) {
	topicID := 523452
	isTopicMessage := false

	_, ok := (&Adapter{}).TopicLifecycleFromEvent(&events.MessageEvent{
		Type: messagetype.ForumTopicCreated,
		Message: &client.Message{
			MessageId:       218,
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
		},
	})
	if ok {
		t.Fatal("TopicLifecycleFromEvent() ok = true, want false for private non-topic chat")
	}
}

func TestTopicLifecycleFromEvent_AcceptsSupergroup(t *testing.T) {
	topicID := 77

	got, ok := (&Adapter{}).TopicLifecycleFromEvent(&events.MessageEvent{
		Type: messagetype.ForumTopicCreated,
		Message: &client.Message{
			MessageId:       42,
			MessageThreadId: &topicID,
			Chat: client.Chat{
				Id:   9001,
				Type: "supergroup",
			},
			From: &client.User{Id: 101},
		},
	})
	if !ok {
		t.Fatal("TopicLifecycleFromEvent() ok = false, want true for supergroup")
	}
	if got.TopicID != 77 {
		t.Fatalf("topic_id = %d, want 77", got.TopicID)
	}
	if got.Locator.SessionID != "tg-9001-77" {
		t.Fatalf("session_id = %q, want tg-9001-77", got.Locator.SessionID)
	}
}

func TestTopicLifecycleFromEvent_AcceptsPrivateTopic(t *testing.T) {
	topicID := 523452
	isTopicMessage := true

	got, ok := (&Adapter{}).TopicLifecycleFromEvent(&events.MessageEvent{
		Type: messagetype.ForumTopicCreated,
		Message: &client.Message{
			MessageId:       218,
			MessageThreadId: &topicID,
			IsTopicMessage:  &isTopicMessage,
			Chat: client.Chat{
				Id:   2317500,
				Type: "private",
			},
			From: &client.User{Id: 2317500},
		},
	})
	if !ok {
		t.Fatal("TopicLifecycleFromEvent() ok = false, want true for private topic")
	}
	if got.TopicID != 523452 {
		t.Fatalf("topic_id = %d, want 523452", got.TopicID)
	}
	if got.Locator.SessionID != "tg-2317500-523452" {
		t.Fatalf("session_id = %q, want tg-2317500-523452", got.Locator.SessionID)
	}
}

func textPtr(s string) *string {
	return &s
}
