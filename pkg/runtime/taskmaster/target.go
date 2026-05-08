package taskmaster

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
)

var ErrUnsupported = errors.New("unsupported locator operation")

type Target interface {
	Supports(locator Locator) bool
	DispatchTask(ctx context.Context, task Task) error
}

type targetRegistry struct {
	targets []Target
}

func newTargetRegistry(targets []Target) targetRegistry {
	cloned := make([]Target, 0, len(targets))
	for _, target := range targets {
		if target != nil {
			cloned = append(cloned, target)
		}
	}
	return targetRegistry{targets: cloned}
}

func (r targetRegistry) supports(locator Locator) bool {
	for _, target := range r.targets {
		if target.Supports(locator) {
			return true
		}
	}
	return false
}

func (r targetRegistry) dispatchTask(ctx context.Context, task Task) error {
	supported := false
	for _, target := range r.targets {
		if !target.Supports(task.Locator) {
			continue
		}
		supported = true
		err := target.DispatchTask(ctx, task)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrUnsupported) {
			continue
		}
		return err
	}
	if supported {
		return fmt.Errorf("unsupported dispatch for locator %s", task.Locator)
	}
	return fmt.Errorf("no target for locator %s", task.Locator)
}

type CLILogTarget struct {
	logger zerolog.Logger
}

func NewCLILogTarget(logger zerolog.Logger) Target {
	return CLILogTarget{logger: logger}
}

func (t CLILogTarget) Supports(locator Locator) bool {
	return locator.Class == LocatorClassIntegration &&
		locator.Transport == LocatorTransportCLI &&
		locator.Key == CLILogKey
}

func (t CLILogTarget) DispatchTask(_ context.Context, task Task) error {
	message := task.Content
	if message == "" {
		message = "(empty task content)"
	}
	t.logger.Info().
		Str("session_id", task.SessionID).
		Str("locator", task.Locator.String()).
		Str("message_text", message).
		Msg("cli log delivered")
	return nil
}
