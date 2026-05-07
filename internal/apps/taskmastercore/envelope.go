package taskmastercore

import (
	"errors"
	"fmt"
	"strings"
)

const (
	LocatorClassAgent       = "agent"
	LocatorClassAlias       = "alias"
	LocatorClassHuman       = "human"
	LocatorClassIntegration = "integration"

	LocatorTransportCLI      = "cli"
	LocatorTransportLocal    = "local"
	LocatorTransportTelegram = "telegram"
	LocatorTransportTimer    = "timer"
	LocatorTransportWhatsApp = "whatsapp"

	CLIInputKey     = "input"
	CLILogKey       = "log"
	DefaultTimerKey = "default"
)

type Locator struct {
	Class     string         `json:"class"`
	Transport string         `json:"transport"`
	Key       string         `json:"key"`
	Address   map[string]any `json:"address,omitempty"`
}

func NewLocator(locatorClass string, transport string, key string) Locator {
	return Locator{
		Class:     strings.ToLower(strings.TrimSpace(locatorClass)),
		Transport: strings.ToLower(strings.TrimSpace(transport)),
		Key:       strings.TrimSpace(key),
	}
}

func NewAgentLocator(id string) Locator {
	return NewLocator(LocatorClassAgent, LocatorTransportLocal, strings.ToLower(strings.TrimSpace(id)))
}

func NewCLIInputLocator() Locator {
	return NewLocator(LocatorClassIntegration, LocatorTransportCLI, CLIInputKey)
}

func NewCLILogLocator() Locator {
	return NewLocator(LocatorClassIntegration, LocatorTransportCLI, CLILogKey)
}

func NewTimerSourceLocator() Locator {
	return NewLocator(LocatorClassIntegration, LocatorTransportTimer, DefaultTimerKey)
}

func NewTelegramHumanLocator(chatID int64, topicID int) Locator {
	locator := NewLocator(LocatorClassHuman, LocatorTransportTelegram, fmt.Sprintf("%d:%d", chatID, topicID))
	locator.Address = map[string]any{
		"chat_id":  chatID,
		"topic_id": topicID,
	}
	return locator
}

func NewWhatsAppHumanLocator(phoneNumberID string) Locator {
	locator := NewLocator(LocatorClassHuman, LocatorTransportWhatsApp, strings.TrimSpace(phoneNumberID))
	locator.Address = map[string]any{
		"phone_number_id": strings.TrimSpace(phoneNumberID),
	}
	return locator
}

func cloneAddress(address map[string]any) map[string]any {
	if len(address) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(address))
	for key, value := range address {
		cloned[key] = value
	}
	return cloned
}

func normalizeLocator(locator Locator) (Locator, error) {
	normalized := NewLocator(locator.Class, locator.Transport, locator.Key)
	normalized.Address = cloneAddress(locator.Address)
	if normalized.Class == "" {
		return Locator{}, errors.New("locator.class is required")
	}
	if normalized.Transport == "" {
		return Locator{}, errors.New("locator.transport is required")
	}
	if normalized.Key == "" {
		return Locator{}, errors.New("locator.key is required")
	}
	return normalized, nil
}

func normalizeReportLocator(reportTo *Locator, defaultReportTo Locator) (Locator, error) {
	if reportTo == nil {
		return defaultReportTo, nil
	}
	return normalizeLocator(*reportTo)
}

func locatorKey(locator Locator) string {
	return fmt.Sprintf("%s:%s:%s", locator.Class, locator.Transport, locator.Key)
}

func locatorString(locator Locator) string {
	return locatorKey(locator)
}

func locatorPtrString(locator *Locator) string {
	if locator == nil {
		return ""
	}
	return locatorString(*locator)
}

func isBuiltInSourceLocator(locator Locator) bool {
	if locator.Class != LocatorClassIntegration {
		return false
	}
	if locator.Transport == LocatorTransportTimer {
		return true
	}
	return locator.Transport == LocatorTransportCLI && locator.Key == CLIInputKey
}
