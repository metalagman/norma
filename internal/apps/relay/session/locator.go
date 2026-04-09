package session

import (
	"encoding/json"
	"fmt"
	"strings"

	relaystate "github.com/normahq/norma/internal/apps/relay/state"
)

const telegramSessionIDPrefix = "tg"

// SessionLocator identifies a relay session without exposing channel-specific
// tuple parameters through the manager API.
type SessionLocator struct {
	ChannelType string
	AddressKey  string
	AddressJSON string
	SessionID   string
}

// TelegramAddress is the Telegram-specific address shape carried at the edge.
type TelegramAddress struct {
	ChatID  int64 `json:"chat_id"`
	TopicID int   `json:"topic_id"`
}

// NewTelegramSessionLocator builds a channel-aware locator for Telegram.
func NewTelegramSessionLocator(chatID int64, topicID int) SessionLocator {
	address := TelegramAddress{ChatID: chatID, TopicID: topicID}
	raw, _ := json.Marshal(address)
	return SessionLocator{
		ChannelType: relaystate.ChannelTypeTelegram,
		AddressKey:  fmt.Sprintf("%d:%d", chatID, topicID),
		AddressJSON: string(raw),
		SessionID:   fmt.Sprintf("%s-%d-%d", telegramSessionIDPrefix, chatID, topicID),
	}
}

// LocatorFromRecord reconstructs a session locator from persisted metadata.
func LocatorFromRecord(record relaystate.SessionRecord) (SessionLocator, error) {
	locator := SessionLocator{
		ChannelType: strings.TrimSpace(record.ChannelType),
		AddressKey:  strings.TrimSpace(record.AddressKey),
		AddressJSON: strings.TrimSpace(record.AddressJSON),
		SessionID:   strings.TrimSpace(record.SessionID),
	}
	if locator.ChannelType == "" {
		return SessionLocator{}, fmt.Errorf("channel_type is required")
	}
	if locator.AddressKey == "" {
		return SessionLocator{}, fmt.Errorf("address_key is required")
	}
	if locator.AddressJSON == "" {
		return SessionLocator{}, fmt.Errorf("address_json is required")
	}
	if locator.SessionID == "" {
		return SessionLocator{}, fmt.Errorf("session_id is required")
	}
	return locator, nil
}

// TelegramAddress decodes the locator into the current Telegram edge shape.
func (l SessionLocator) TelegramAddress() (TelegramAddress, bool, error) {
	if l.ChannelType != relaystate.ChannelTypeTelegram {
		return TelegramAddress{}, false, nil
	}

	var address TelegramAddress
	if err := json.Unmarshal([]byte(l.AddressJSON), &address); err != nil {
		return TelegramAddress{}, true, fmt.Errorf("decode telegram address: %w", err)
	}
	return address, true, nil
}
