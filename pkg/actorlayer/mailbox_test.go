package actorlayer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBoundedMailboxCloseUnblocksBlockingEnqueue(t *testing.T) {
	t.Parallel()

	mb, err := NewBoundedMailboxFactory(BoundedMailboxConfig{
		Capacity:   1,
		FullPolicy: BlockUntilSpace,
	}).NewMailbox("a1")
	if err != nil {
		t.Fatalf("NewMailbox() error = %v", err)
	}

	if err := mb.Enqueue(context.Background(), Envelope{Payload: "first"}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- mb.Enqueue(context.Background(), Envelope{Payload: "blocked"})
	}()

	time.Sleep(20 * time.Millisecond)
	if err := mb.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case got := <-done:
		if !errors.Is(got, ErrMailboxClosed) {
			t.Fatalf("blocked enqueue error = %v, want %v", got, ErrMailboxClosed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked enqueue did not unblock on Close")
	}
}

func TestBoundedMailboxDequeueDrainsBufferedBeforeClosed(t *testing.T) {
	t.Parallel()

	mb, err := NewBoundedMailboxFactory(BoundedMailboxConfig{
		Capacity:   4,
		FullPolicy: FailFast,
	}).NewMailbox("a2")
	if err != nil {
		t.Fatalf("NewMailbox() error = %v", err)
	}

	if err := mb.Enqueue(context.Background(), Envelope{Payload: "first"}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	if err := mb.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := mb.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue(first) error = %v", err)
	}
	if got.Payload != "first" {
		t.Fatalf("payload = %#v, want %q", got.Payload, "first")
	}

	_, err = mb.Dequeue(context.Background())
	if !errors.Is(err, ErrMailboxClosed) {
		t.Fatalf("second Dequeue() error = %v, want %v", err, ErrMailboxClosed)
	}
}
