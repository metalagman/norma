package taskmastercore

import (
	"errors"
	"fmt"
	"strings"
)

const (
	LocatorTypeAgent       = "agent"
	LocatorTypeHumanOutput = "human_output"

	HumanOutputCurrentLogID = "current_log"
)

type Locator struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func NewAgentLocator(id string) Locator {
	return Locator{Type: LocatorTypeAgent, ID: strings.ToLower(strings.TrimSpace(id))}
}

func NewHumanOutputLocator() Locator {
	return Locator{Type: LocatorTypeHumanOutput, ID: HumanOutputCurrentLogID}
}

func normalizeLocator(locator Locator) (Locator, error) {
	normalized := Locator{
		Type: strings.ToLower(strings.TrimSpace(locator.Type)),
		ID:   strings.ToLower(strings.TrimSpace(locator.ID)),
	}
	if normalized.Type == "" {
		return Locator{}, errors.New("locator.type is required")
	}
	if normalized.ID == "" {
		return Locator{}, errors.New("locator.id is required")
	}
	switch normalized.Type {
	case LocatorTypeAgent, LocatorTypeHumanOutput:
		return normalized, nil
	default:
		return Locator{}, fmt.Errorf("unsupported locator.type %q", locator.Type)
	}
}

func normalizeReportLocator(reportTo *Locator, defaultReportTo Locator) (Locator, error) {
	if reportTo == nil {
		return defaultReportTo, nil
	}
	return normalizeLocator(*reportTo)
}
