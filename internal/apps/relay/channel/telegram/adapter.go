package telegram

import (
	"context"
	"fmt"

	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaysession "github.com/normahq/norma/internal/apps/relay/session"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/messagetype"
	"go.uber.org/fx"
)

// Adapter maps Telegram runtime events and operations to relay session locators.
type Adapter struct {
	messenger *messenger.Messenger
	tgClient  client.ClientWithResponsesInterface
	logger    zerolog.Logger
}

// MessageContext is the relay-facing Telegram message shape.
type MessageContext struct {
	Locator            relaysession.SessionLocator
	ChatID             int64
	TopicID            int
	MessageID          int
	UserID             int64
	Text               string
	HasCommand         bool
	AllowProgressHints bool
}

// CommandContext is the relay-facing Telegram command shape.
type CommandContext struct {
	Locator relaysession.SessionLocator
	ChatID  int64
	TopicID int
	UserID  int64
	Command string
	Args    string
}

// TopicLifecycleContext is the relay-facing Telegram topic lifecycle shape.
type TopicLifecycleContext struct {
	Locator   relaysession.SessionLocator
	ChatID    int64
	TopicID   int
	MessageID int
	UserID    int64
	Type      messagetype.MessageType
}

type AdapterParams struct {
	fx.In

	Messenger *messenger.Messenger
	TGClient  client.ClientWithResponsesInterface
	Logger    zerolog.Logger
}

// NewAdapter creates the Telegram relay adapter.
func NewAdapter(params AdapterParams) *Adapter {
	return &Adapter{
		messenger: params.Messenger,
		tgClient:  params.TGClient,
		logger:    params.Logger.With().Str("component", "relay.channel.telegram").Logger(),
	}
}

// RootLocator returns the root Telegram locator for a chat.
func (a *Adapter) RootLocator(chatID int64) relaysession.SessionLocator {
	return relaysession.NewTelegramSessionLocator(chatID, 0)
}

// MessageContextFromEvent converts a Telegram message event into relay channel context.
func (a *Adapter) MessageContextFromEvent(event *events.MessageEvent) (MessageContext, bool) {
	if event == nil || event.Message == nil || event.Message.From == nil {
		return MessageContext{}, false
	}

	topicID := a.topicIDFromMessage(event.Message)

	text := ""
	if event.Message.Text != nil {
		text = *event.Message.Text
	}

	return MessageContext{
		Locator:            relaysession.NewTelegramSessionLocator(event.Message.Chat.Id, topicID),
		ChatID:             event.Message.Chat.Id,
		TopicID:            topicID,
		MessageID:          event.Message.MessageId,
		UserID:             event.Message.From.Id,
		Text:               text,
		HasCommand:         hasCommandEntity(event.Message),
		AllowProgressHints: event.Message.Chat.Type == "private",
	}, true
}

// CommandContextFromEvent converts a Telegram command event into relay channel context.
func (a *Adapter) CommandContextFromEvent(event *events.CommandEvent) (CommandContext, bool) {
	if event == nil || event.Message == nil || event.Message.From == nil {
		return CommandContext{}, false
	}

	topicID := a.topicIDFromMessage(event.Message)

	return CommandContext{
		Locator: relaysession.NewTelegramSessionLocator(event.Message.Chat.Id, topicID),
		ChatID:  event.Message.Chat.Id,
		TopicID: topicID,
		UserID:  event.Message.From.Id,
		Command: event.Command,
		Args:    event.Args,
	}, true
}

// TopicLifecycleFromEvent converts a Telegram topic lifecycle event into relay channel context.
func (a *Adapter) TopicLifecycleFromEvent(event *events.MessageEvent) (TopicLifecycleContext, bool) {
	if event == nil || event.Message == nil || event.Message.MessageThreadId == nil {
		return TopicLifecycleContext{}, false
	}
	if !isTopicMessage(event.Message) {
		a.logger.Debug().
			Str("chat_type", event.Message.Chat.Type).
			Int("message_thread_id", *event.Message.MessageThreadId).
			Msg("ignoring topic lifecycle event for non-topic message")
		return TopicLifecycleContext{}, false
	}

	topicID := *event.Message.MessageThreadId
	userID := int64(0)
	if event.Message.From != nil {
		userID = event.Message.From.Id
	}

	return TopicLifecycleContext{
		Locator:   relaysession.NewTelegramSessionLocator(event.Message.Chat.Id, topicID),
		ChatID:    event.Message.Chat.Id,
		TopicID:   topicID,
		MessageID: event.Message.MessageId,
		UserID:    userID,
		Type:      event.Type,
	}, true
}

// SendPlain sends a plain text reply to the locator.
func (a *Adapter) SendPlain(ctx context.Context, locator relaysession.SessionLocator, text string) error {
	chatID, topicID, err := telegramTuple(locator)
	if err != nil {
		return err
	}
	return a.messenger.SendPlain(ctx, chatID, text, topicID)
}

// SendMarkdown sends a Markdown reply to the locator.
func (a *Adapter) SendMarkdown(ctx context.Context, locator relaysession.SessionLocator, text string) error {
	chatID, topicID, err := telegramTuple(locator)
	if err != nil {
		return err
	}
	return a.messenger.SendMarkdown(ctx, chatID, text, topicID)
}

// SendDraftPlain updates a draft message for the locator.
func (a *Adapter) SendDraftPlain(ctx context.Context, locator relaysession.SessionLocator, draftID int, text string) error {
	chatID, topicID, err := telegramTuple(locator)
	if err != nil {
		return err
	}
	return a.messenger.SendDraftPlain(ctx, chatID, draftID, text, topicID)
}

// SendTyping sends a typing chat action to the locator chat/topic.
func (a *Adapter) SendTyping(ctx context.Context, locator relaysession.SessionLocator) error {
	chatID, topicID, err := telegramTuple(locator)
	if err != nil {
		return err
	}
	return a.messenger.SendChatAction(ctx, chatID, topicID, "typing")
}

// CreateTopicLocator creates a Telegram forum topic and returns the relay locator for it.
func (a *Adapter) CreateTopicLocator(ctx context.Context, chatID int64, topicName string) (relaysession.SessionLocator, error) {
	createTopicResp, err := a.tgClient.CreateForumTopicWithResponse(ctx, client.CreateForumTopicJSONRequestBody{
		ChatId: chatID,
		Name:   topicName,
	})
	if err != nil {
		return relaysession.SessionLocator{}, fmt.Errorf("creating forum topic: %w", err)
	}
	if createTopicResp.JSON200 == nil {
		return relaysession.SessionLocator{}, fmt.Errorf("failed to create forum topic: %s", createTopicResp.Status())
	}

	return relaysession.NewTelegramSessionLocator(chatID, createTopicResp.JSON200.Result.MessageThreadId), nil
}

// Close closes a Telegram forum topic for the locator. Root locators are ignored.
func (a *Adapter) Close(ctx context.Context, locator relaysession.SessionLocator) error {
	chatID, topicID, err := telegramTuple(locator)
	if err != nil {
		return err
	}
	if topicID == 0 {
		return nil
	}

	closeResp, err := a.tgClient.CloseForumTopicWithResponse(ctx, client.CloseForumTopicJSONRequestBody{
		ChatId:          chatID,
		MessageThreadId: topicID,
	})
	if err != nil {
		return fmt.Errorf("closing forum topic: %w", err)
	}
	if closeResp.JSON200 == nil {
		return fmt.Errorf("failed to close forum topic: %s", closeResp.Status())
	}
	return nil
}

func telegramTuple(locator relaysession.SessionLocator) (int64, int, error) {
	address, ok, err := locator.TelegramAddress()
	if err != nil {
		return 0, 0, fmt.Errorf("decode telegram locator %q: %w", locator.SessionID, err)
	}
	if !ok {
		return 0, 0, fmt.Errorf("unsupported channel type %q", locator.ChannelType)
	}
	return address.ChatID, address.TopicID, nil
}

func hasCommandEntity(msg *client.Message) bool {
	if msg == nil || msg.Entities == nil {
		return false
	}
	for _, entity := range *msg.Entities {
		if entity.Type == "bot_command" {
			return true
		}
	}
	return false
}

func (a *Adapter) topicIDFromMessage(msg *client.Message) int {
	if msg == nil || msg.MessageThreadId == nil {
		return 0
	}
	if !isTopicMessage(msg) {
		a.logger.Debug().
			Str("chat_type", msg.Chat.Type).
			Int("message_thread_id", *msg.MessageThreadId).
			Msg("ignoring message_thread_id for non-topic message")
		return 0
	}
	return *msg.MessageThreadId
}

func isTopicMessage(msg *client.Message) bool {
	if msg == nil || msg.MessageThreadId == nil {
		return false
	}
	if msg.IsTopicMessage != nil {
		return *msg.IsTopicMessage
	}
	// Fallback for payloads that omit is_topic_message: if Telegram sent a
	// message_thread_id, treat it as a topic/thread-scoped message.
	return true
}
