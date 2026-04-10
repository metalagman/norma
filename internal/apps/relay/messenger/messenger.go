package messenger

import (
	"bytes"
	"context"
	"fmt"
	gohtml "html"
	"strings"
	"time"

	tgmd "github.com/Mad-Pixels/goldmark-tgmd"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
)

// Messenger handles all Telegram message sending for the relay.
type Messenger struct {
	client client.ClientWithResponsesInterface
	logger zerolog.Logger
}

// NewMessenger creates a new Messenger.
func NewMessenger(client client.ClientWithResponsesInterface, logger zerolog.Logger) *Messenger {
	return &Messenger{
		client: client,
		logger: logger.With().Str("component", "relay.messenger").Logger(),
	}
}

// SendDraftPlain sends a plain-text draft (no parse_mode).
func (m *Messenger) SendDraftPlain(ctx context.Context, chatID int64, draftID int, text string, topicID int) error {
	m.logger.Debug().
		Int64("chat_id", chatID).
		Int("draft_id", draftID).
		Str("text_escaped", gohtml.EscapeString(text)).
		Msg("sending plain draft")
	req := client.SendMessageDraftJSONRequestBody{
		ChatId:  chatID,
		DraftId: draftID,
		Text:    text,
	}
	if topicID != 0 {
		req.MessageThreadId = &topicID
	}

	resp, err := m.client.SendMessageDraftWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("sending plain draft to chat %d: %w", chatID, err)
	}
	if resp.JSON400 != nil {
		return fmt.Errorf("sending plain draft to chat %d: %s", chatID, resp.JSON400.Description)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("sending plain draft to chat %d: no response body", chatID)
	}
	return nil
}

// SendPlain sends a plain-text message.
func (m *Messenger) SendPlain(ctx context.Context, chatID int64, text string, topicID int) error {
	req := client.SendMessageJSONRequestBody{
		ChatId: chatID,
		Text:   text,
	}
	if topicID != 0 {
		req.MessageThreadId = &topicID
	}
	_, err := m.client.SendMessageWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("sending message to chat %d: %w", chatID, err)
	}
	return nil
}

// SendMarkdown converts standard Markdown to Telegram MarkdownV2 and sends.
func (m *Messenger) SendMarkdown(ctx context.Context, chatID int64, text string, topicID int) error {
	var buf bytes.Buffer
	md := tgmd.TGMD()
	if err := md.Convert([]byte(text), &buf); err != nil {
		m.logger.Warn().Err(err).Msg("failed to convert markdown to telegram format, falling back to escaped literal")
		return m.sendMessageWithMode(ctx, chatID, escapeMarkdownV2(text), topicID, "MarkdownV2", "send message with MarkdownV2")
	}
	return m.sendMessageWithMode(ctx, chatID, buf.String(), topicID, "MarkdownV2", "send message with MarkdownV2")
}

func (m *Messenger) sendMessageWithMode(ctx context.Context, chatID int64, text string, topicID int, mode, logMsg string) error {
	m.logger.Debug().
		Int64("chat_id", chatID).
		Str("mode", mode).
		Str("text_escaped", gohtml.EscapeString(text)).
		Msg("sending telegram message")
	req := client.SendMessageJSONRequestBody{
		ChatId: chatID,
		Text:   text,
	}
	if mode != "" {
		req.ParseMode = &mode
	}
	if topicID != 0 {
		req.MessageThreadId = &topicID
	}
	resp, err := m.client.SendMessageWithResponse(ctx, req)
	if err != nil {
		m.logger.Warn().Err(err).Int64("chat_id", chatID).Msg(logMsg + " failed, retrying without parse_mode")
		req.ParseMode = nil
		resp, err = m.client.SendMessageWithResponse(ctx, req)
		if err != nil {
			return fmt.Errorf("%s to chat %d: %w", logMsg, chatID, err)
		}
	}
	if resp.JSON400 != nil {
		return fmt.Errorf("%s to chat %d: %s", logMsg, chatID, resp.JSON400.Description)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("%s to chat %d: no response body", logMsg, chatID)
	}
	return nil
}

// SendChatAction sends a chat action (e.g., "typing").
func (m *Messenger) SendChatAction(ctx context.Context, chatID int64, topicID int, action string) error {
	if chatID == 0 {
		return nil
	}
	req := client.SendChatActionJSONRequestBody{
		ChatId: chatID,
		Action: action,
	}
	if topicID != 0 {
		req.MessageThreadId = &topicID
	}

	resp, err := m.client.SendChatActionWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("sending chat action %q to chat %d: %w", action, chatID, err)
	}
	if resp.JSON400 != nil {
		return fmt.Errorf("sending chat action %q to chat %d: %s", action, chatID, resp.JSON400.Description)
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("sending chat action %q to chat %d: no response body", action, chatID)
	}
	return nil
}

// KeepTyping sends typing action every 4 seconds until context is canceled.
func (m *Messenger) KeepTyping(ctx context.Context, chatID int64, topicID int) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.SendChatAction(ctx, chatID, topicID, "typing"); err != nil {
				m.logger.Warn().Err(err).Int64("chat_id", chatID).Int("topic_id", topicID).Msg("failed to send typing chat action")
			}
		}
	}
}

func escapeMarkdownV2(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
		"\\", "\\\\",
	)
	return replacer.Replace(text)
}
