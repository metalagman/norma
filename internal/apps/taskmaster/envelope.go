package taskmaster

import (
	"errors"
	"fmt"
	"strings"
)

const (
	taskmasterAgentID = "taskmaster"
	locatorTypeAgent  = "agent"
)

type taskLocator struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func newAgentLocator(id string) taskLocator {
	return taskLocator{Type: locatorTypeAgent, ID: strings.ToLower(strings.TrimSpace(id))}
}

func normalizeLocator(locator taskLocator) (taskLocator, error) {
	normalized := taskLocator{
		Type: strings.ToLower(strings.TrimSpace(locator.Type)),
		ID:   strings.ToLower(strings.TrimSpace(locator.ID)),
	}
	if normalized.Type == "" {
		return taskLocator{}, errors.New("locator.type is required")
	}
	if normalized.ID == "" {
		return taskLocator{}, errors.New("locator.id is required")
	}
	if normalized.Type != locatorTypeAgent {
		return taskLocator{}, fmt.Errorf("unsupported locator.type %q", locator.Type)
	}
	return normalized, nil
}

func normalizeReplyLocator(replyTo *taskLocator) (taskLocator, error) {
	if replyTo == nil {
		return newAgentLocator(taskmasterAgentID), nil
	}
	return normalizeLocator(*replyTo)
}
