package taskmastercore

import (
	"context"
	"errors"
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

var ErrUnsupported = errors.New("unsupported locator operation")

type Provider interface {
	Supports(locator Locator) bool
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

func (r providerRegistry) supports(locator Locator) bool {
	for _, provider := range r.providers {
		if provider.Supports(locator) {
			return true
		}
	}
	return false
}

func (r providerRegistry) dispatchTask(ctx context.Context, req DispatchRequest) error {
	supported := false
	for _, provider := range r.providers {
		if !provider.Supports(req.Locator) {
			continue
		}
		supported = true
		err := provider.DispatchTask(ctx, req)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrUnsupported) {
			continue
		}
		return err
	}
	if supported {
		return fmt.Errorf("unsupported dispatch for locator %s", locatorKey(req.Locator))
	}
	return fmt.Errorf("no provider for locator %s", locatorKey(req.Locator))
}

func (r providerRegistry) deliverReport(ctx context.Context, req ReportRequest) error {
	supported := false
	for _, provider := range r.providers {
		if !provider.Supports(req.ReportTo) {
			continue
		}
		supported = true
		err := provider.DeliverReport(ctx, req)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrUnsupported) {
			continue
		}
		return err
	}
	if supported {
		return fmt.Errorf("unsupported report for locator %s", locatorKey(req.ReportTo))
	}
	return fmt.Errorf("no provider for report_to %s", locatorKey(req.ReportTo))
}

type cliLogProvider struct {
	logger zerolog.Logger
}

func NewCLILogProvider(logger zerolog.Logger) Provider {
	return cliLogProvider{logger: logger}
}

func (p cliLogProvider) Supports(locator Locator) bool {
	return locator.Class == LocatorClassIntegration &&
		locator.Transport == LocatorTransportCLI &&
		locator.Key == CLILogKey
}

func (p cliLogProvider) DispatchTask(_ context.Context, req DispatchRequest) error {
	return ErrUnsupported
}

func (p cliLogProvider) DeliverReport(_ context.Context, req ReportRequest) error {
	message := strings.TrimSpace(req.Prompt)
	if message == "" {
		message = "(empty report)"
	}
	p.logger.Info().
		Str("task_id", req.TaskID).
		Str("session_id", req.SessionID).
		Str("source_locator", locatorString(req.SourceLocator)).
		Str("report_to", locatorString(req.ReportTo)).
		Str("message_text", message).
		Msg("cli log delivered")
	return nil
}
