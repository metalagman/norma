package session

import "fmt"

// SessionContext carries the channel locator plus the transport actor identity
// used to bind the underlying ADK session.
type SessionContext struct {
	Locator SessionLocator
	UserID  string
}

// TelegramUserID returns the canonical Telegram-backed ADK user ID.
func TelegramUserID(userID int64) string {
	return fmt.Sprintf("%s-%d", telegramSessionIDPrefix, userID)
}
