package actorlayer

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Failure captures a behavior error or panic with message context.
type Failure struct {
	Actor   Ref
	Message Envelope
	Err     error
	Panic   any
	At      time.Time
}

// Directive is the supervisor decision returned after a failure.
type Directive int

const (
	// Resume keeps the current behavior instance and continues processing.
	Resume Directive = iota
	// Restart rebuilds actor behavior using Props.NewBehavior.
	Restart
	// Stop terminates the actor.
	Stop
	// Escalate delegates the failure to a parent/superior policy.
	Escalate
)

// SupervisionContext contains runtime context for supervisor decisions.
type SupervisionContext struct {
	System *System
}

// SupervisorStrategy decides actor action for a message failure.
type SupervisorStrategy interface {
	Decide(ctx SupervisionContext, failure Failure) Directive
}

// DefaultSupervisor provides conservative failure handling defaults.
type DefaultSupervisor struct{}

// Decide chooses a directive based on panic/error class.
func (DefaultSupervisor) Decide(_ SupervisionContext, failure Failure) Directive {
	if failure.Panic != nil {
		return Restart
	}
	if errors.Is(failure.Err, context.Canceled) {
		return Resume
	}
	if errors.Is(failure.Err, context.DeadlineExceeded) {
		return Resume
	}
	if failure.Err != nil {
		return Resume
	}
	return Resume
}

// ThresholdSupervisorConfig configures restart-threshold based supervision.
type ThresholdSupervisorConfig struct {
	Base        SupervisorStrategy
	MaxRestarts int
	Window      time.Duration
	OnExceeded  Directive
}

// ThresholdSupervisor escalates or stops an actor after too many restart-worthy
// failures within the configured time window.
type ThresholdSupervisor struct {
	base        SupervisorStrategy
	maxRestarts int
	window      time.Duration
	onExceeded  Directive

	mu       sync.Mutex
	restarts map[ActorID][]time.Time
}

// NewThresholdSupervisor returns a supervisor that limits restart bursts.
func NewThresholdSupervisor(cfg ThresholdSupervisorConfig) *ThresholdSupervisor {
	base := cfg.Base
	if base == nil {
		base = DefaultSupervisor{}
	}
	onExceeded := cfg.OnExceeded
	if onExceeded == Resume || onExceeded == Restart {
		onExceeded = Escalate
	}
	return &ThresholdSupervisor{
		base:        base,
		maxRestarts: cfg.MaxRestarts,
		window:      cfg.Window,
		onExceeded:  onExceeded,
		restarts:    make(map[ActorID][]time.Time),
	}
}

// Decide applies the base strategy and enforces restart thresholds.
func (s *ThresholdSupervisor) Decide(ctx SupervisionContext, failure Failure) Directive {
	baseDirective := s.base.Decide(ctx, failure)
	if s.maxRestarts <= 0 {
		return baseDirective
	}
	if baseDirective != Restart {
		return baseDirective
	}

	at := failure.At
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	history := s.restarts[failure.Actor.ID()]
	if s.window > 0 {
		cutoff := at.Add(-s.window)
		dst := history[:0]
		for _, ts := range history {
			if !ts.Before(cutoff) {
				dst = append(dst, ts)
			}
		}
		history = dst
	}

	history = append(history, at)
	s.restarts[failure.Actor.ID()] = history
	if len(history) > s.maxRestarts {
		return s.onExceeded
	}
	return baseDirective
}
