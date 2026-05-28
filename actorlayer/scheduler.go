package actorlayer

import (
	"context"
	"sync"
	"time"
)

// Scheduled represents a delayed message delivery.
type Scheduled interface {
	// Cancel stops the scheduled delivery if it has not fired yet.
	Cancel() bool
	// Done is closed when scheduling completes, returning the final send result.
	Done() <-chan error
}

type scheduledTell struct {
	mu     sync.Mutex
	timer  *time.Timer
	done   chan error
	closed chan struct{}
	once   sync.Once
}

func (s *scheduledTell) Cancel() bool {
	s.mu.Lock()
	timer := s.timer
	s.mu.Unlock()
	if timer == nil {
		return false
	}
	if !timer.Stop() {
		return false
	}
	s.finish(ErrScheduleCanceled)
	return true
}

func (s *scheduledTell) Done() <-chan error {
	return s.done
}

func (s *scheduledTell) finish(err error) {
	s.once.Do(func() {
		s.done <- err
		close(s.done)
		close(s.closed)
	})
}

// TellAfter schedules a Tell delivery after delay.
func (s *System) TellAfter(ctx context.Context, delay time.Duration, to Ref, payload any, opts ...TellOption) (Scheduled, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !to.validFor(s) {
		return nil, ErrActorNotFound
	}
	if delay < 0 {
		delay = 0
	}

	sched := &scheduledTell{
		done:   make(chan error, 1),
		closed: make(chan struct{}),
	}
	timer := time.AfterFunc(delay, func() {
		sched.finish(s.Tell(ctx, to, payload, opts...))
	})
	sched.mu.Lock()
	sched.timer = timer
	sched.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			sched.mu.Lock()
			t := sched.timer
			sched.mu.Unlock()
			if t != nil && t.Stop() {
				sched.finish(ctx.Err())
			}
		case <-sched.closed:
		}
	}()

	return sched, nil
}
