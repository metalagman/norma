package goalkeeper

import (
	"errors"
	"fmt"
	"strings"
)

const (
	goalkeeperAgentID = "goalkeeper"
	locatorTypeAgent  = "agent"
)

type jobLocator struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func newAgentLocator(id string) jobLocator {
	return jobLocator{Type: locatorTypeAgent, ID: strings.ToLower(strings.TrimSpace(id))}
}

func normalizeLocator(locator jobLocator) (jobLocator, error) {
	normalized := jobLocator{
		Type: strings.ToLower(strings.TrimSpace(locator.Type)),
		ID:   strings.ToLower(strings.TrimSpace(locator.ID)),
	}
	if normalized.Type == "" {
		return jobLocator{}, errors.New("locator.type is required")
	}
	if normalized.ID == "" {
		return jobLocator{}, errors.New("locator.id is required")
	}
	if normalized.Type != locatorTypeAgent {
		return jobLocator{}, fmt.Errorf("unsupported locator.type %q", locator.Type)
	}
	return normalized, nil
}

func normalizeReplyLocator(replyTo *jobLocator) (jobLocator, error) {
	if replyTo == nil {
		return newAgentLocator(goalkeeperAgentID), nil
	}
	return normalizeLocator(*replyTo)
}
