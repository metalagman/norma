package taskmastercore

import (
	"errors"
	"fmt"
	"strings"
)

const (
	LocatorTypeAgent  = "agent"
	LocatorTypeSink   = "sink"
	LocatorTypeSystem = "system"

	LocatorKindLocal       = "local"
	LocatorKindHumanOutput = "human_output"
	LocatorKindCLI         = "cli"
	LocatorKindTimer       = "timer"

	HumanOutputCurrentLogID = "current_log"
)

type Locator struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func NewLocator(locatorType string, kind string, id string) Locator {
	return Locator{
		Type: strings.ToLower(strings.TrimSpace(locatorType)),
		Kind: strings.ToLower(strings.TrimSpace(kind)),
		ID:   strings.TrimSpace(id),
	}
}

func NewAgentLocator(id string) Locator {
	return NewLocator(LocatorTypeAgent, LocatorKindLocal, strings.ToLower(strings.TrimSpace(id)))
}

func NewHumanOutputLocator() Locator {
	return NewLocator(LocatorTypeSink, LocatorKindHumanOutput, HumanOutputCurrentLogID)
}

func NewCLILocator(id string) Locator {
	return NewLocator(LocatorTypeSystem, LocatorKindCLI, id)
}

func NewTimerLocator(id string) Locator {
	return NewLocator(LocatorTypeSystem, LocatorKindTimer, id)
}

func normalizeLocator(locator Locator) (Locator, error) {
	normalized := NewLocator(locator.Type, locator.Kind, locator.ID)
	if normalized.Type == "" {
		return Locator{}, errors.New("locator.type is required")
	}
	if normalized.Kind == "" {
		return Locator{}, errors.New("locator.kind is required")
	}
	if normalized.ID == "" {
		return Locator{}, errors.New("locator.id is required")
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
	return fmt.Sprintf("%s:%s:%s", locator.Type, locator.Kind, locator.ID)
}
