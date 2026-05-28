package actorlayer

import (
	"context"
	"errors"
	"sync"
)

// Mailbox is the actor queue abstraction used by each actor cell.
type Mailbox interface {
	Enqueue(ctx context.Context, env Envelope) error
	Dequeue(ctx context.Context) (Envelope, error)
	Len() int
	Close() error
}

// MailboxFactory creates a mailbox instance for a specific actor.
type MailboxFactory interface {
	NewMailbox(actorID ActorID) (Mailbox, error)
}

// BoundedMailboxConfig configures bounded FIFO mailbox behavior.
type BoundedMailboxConfig struct {
	Capacity   int
	FullPolicy MailboxFullPolicy
}

// MailboxFullPolicy defines behavior when enqueue is attempted on a full mailbox.
type MailboxFullPolicy int

const (
	// FailFast returns ErrMailboxFull when the mailbox is full.
	FailFast MailboxFullPolicy = iota
	// BlockUntilSpace blocks until space is available or context closes.
	BlockUntilSpace
	// DropNewest discards the new message when the mailbox is full.
	DropNewest
	// DropOldest removes one queued message and enqueues the new message.
	DropOldest
)

type boundedMailboxFactory struct {
	cfg BoundedMailboxConfig
}

// NewBoundedMailboxFactory returns a mailbox factory backed by bounded in-memory channels.
func NewBoundedMailboxFactory(cfg BoundedMailboxConfig) MailboxFactory {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 64
	}
	return &boundedMailboxFactory{cfg: cfg}
}

func (f *boundedMailboxFactory) NewMailbox(_ ActorID) (Mailbox, error) {
	return &boundedMailbox{
		ch:         make(chan Envelope, f.cfg.Capacity),
		closedCh:   make(chan struct{}),
		fullPolicy: f.cfg.FullPolicy,
	}, nil
}

type boundedMailbox struct {
	ch         chan Envelope
	closedCh   chan struct{}
	fullPolicy MailboxFullPolicy

	closeOnce sync.Once
	closeMu   sync.RWMutex
	closed    bool
}

func (b *boundedMailbox) Enqueue(ctx context.Context, env Envelope) (err error) {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if b.isClosed() {
		return ErrMailboxClosed
	}

	switch b.fullPolicy {
	case FailFast:
		select {
		case <-b.closedCh:
			return ErrMailboxClosed
		case b.ch <- env:
			return nil
		default:
			return ErrMailboxFull
		}

	case BlockUntilSpace:
		select {
		case <-b.closedCh:
			return ErrMailboxClosed
		case <-ctx.Done():
			return ctx.Err()
		case b.ch <- env:
			return nil
		}

	case DropNewest:
		select {
		case <-b.closedCh:
			return ErrMailboxClosed
		case b.ch <- env:
			return nil
		default:
			return ErrMailboxDrop
		}

	case DropOldest:
		select {
		case <-b.closedCh:
			return ErrMailboxClosed
		case b.ch <- env:
			return nil
		default:
		}

		select {
		case <-b.ch:
		default:
		}

		select {
		case <-b.closedCh:
			return ErrMailboxClosed
		case b.ch <- env:
			return nil
		default:
			return ErrMailboxFull
		}

	default:
		return errors.New("actorlayer: unknown mailbox full policy")
	}
}

func (b *boundedMailbox) Dequeue(ctx context.Context) (Envelope, error) {
	for {
		// Prefer draining queued messages even after close.
		select {
		case env := <-b.ch:
			return env, nil
		default:
		}
		select {
		case env := <-b.ch:
			return env, nil
		case <-b.closedCh:
			return Envelope{}, ErrMailboxClosed
		case <-ctx.Done():
			return Envelope{}, ctx.Err()
		}
	}
}

func (b *boundedMailbox) Len() int {
	return len(b.ch)
}

func (b *boundedMailbox) Close() error {
	b.closeOnce.Do(func() {
		b.closeMu.Lock()
		b.closed = true
		b.closeMu.Unlock()
		close(b.closedCh)
	})
	return nil
}

func (b *boundedMailbox) isClosed() bool {
	b.closeMu.RLock()
	closed := b.closed
	b.closeMu.RUnlock()
	return closed
}
