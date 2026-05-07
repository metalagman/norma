package taskmastercore

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

type DispatchRequest struct {
	TaskID    string
	SessionID string
	Locator   Locator
	ReportTo  Locator
	Prompt    string
}

type ReportRequest struct {
	TaskID        string
	SessionID     string
	SourceTaskID  string
	SourceLocator Locator
	ReportTo      Locator
	Status        string
	Prompt        string
	Output        string
	Error         string
}

type Provider interface {
	SupportsTarget(locator Locator) bool
	SupportsReport(locator Locator) bool
	DispatchTask(ctx context.Context, req DispatchRequest) error
	DeliverReport(ctx context.Context, req ReportRequest) error
}

type providerRegistry struct {
	providers []Provider
}

func newProviderRegistry(providers []Provider) providerRegistry {
	cloned := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			cloned = append(cloned, provider)
		}
	}
	return providerRegistry{providers: cloned}
}

func (r providerRegistry) supportsTarget(locator Locator) bool {
	for _, provider := range r.providers {
		if provider.SupportsTarget(locator) {
			return true
		}
	}
	return false
}

func (r providerRegistry) supportsReport(locator Locator) bool {
	for _, provider := range r.providers {
		if provider.SupportsReport(locator) {
			return true
		}
	}
	return false
}

func (r providerRegistry) dispatchTask(ctx context.Context, req DispatchRequest) error {
	for _, provider := range r.providers {
		if provider.SupportsTarget(req.Locator) {
			return provider.DispatchTask(ctx, req)
		}
	}
	return fmt.Errorf("no provider for locator %s", locatorKey(req.Locator))
}

func (r providerRegistry) deliverReport(ctx context.Context, req ReportRequest) error {
	for _, provider := range r.providers {
		if provider.SupportsReport(req.ReportTo) {
			return provider.DeliverReport(ctx, req)
		}
	}
	return fmt.Errorf("no provider for report_to %s", locatorKey(req.ReportTo))
}

type humanOutputProvider struct {
	logger zerolog.Logger
}

func NewHumanOutputProvider(logger zerolog.Logger) Provider {
	return humanOutputProvider{logger: logger}
}

func (p humanOutputProvider) SupportsTarget(locator Locator) bool {
	return false
}

func (p humanOutputProvider) SupportsReport(locator Locator) bool {
	return locator.Type == LocatorTypeSink && locator.Kind == LocatorKindHumanOutput && locator.ID == HumanOutputCurrentLogID
}

func (p humanOutputProvider) DispatchTask(_ context.Context, req DispatchRequest) error {
	return fmt.Errorf("locator %s does not support task dispatch", locatorKey(req.Locator))
}

func (p humanOutputProvider) DeliverReport(_ context.Context, req ReportRequest) error {
	message := strings.TrimSpace(req.Prompt)
	if message == "" {
		message = "(empty report)"
	}
	p.logger.Info().
		Str("task_id", req.TaskID).
		Str("session_id", req.SessionID).
		Interface("source_locator", req.SourceLocator).
		Str("report_to", locatorKey(req.ReportTo)).
		Str("message_text", message).
		Msg("human output delivered")
	return nil
}
