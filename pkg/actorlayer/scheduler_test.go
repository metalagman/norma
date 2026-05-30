package actorlayer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTellAfterDeliversMessage(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan any, 1)
	ref, err := sys.Spawn(context.Background(), "delayed", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				received <- env.Payload
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	scheduled, err := sys.TellAfter(context.Background(), 20*time.Millisecond, ref, "hello")
	if err != nil {
		t.Fatalf("TellAfter() error = %v", err)
	}

	select {
	case msg := <-received:
		if got, ok := msg.(string); !ok || got != "hello" {
			t.Fatalf("payload = %#v, want %q", msg, "hello")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive delayed message")
	}

	select {
	case doneErr := <-scheduled.Done():
		if doneErr != nil {
			t.Fatalf("scheduled result error = %v, want nil", doneErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive scheduler completion")
	}
}

func TestTellAfterCancelPreventsDelivery(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan struct{}, 1)
	ref, err := sys.Spawn(context.Background(), "cancel", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				received <- struct{}{}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	scheduled, err := sys.TellAfter(context.Background(), 2*time.Second, ref, "nope")
	if err != nil {
		t.Fatalf("TellAfter() error = %v", err)
	}
	if ok := scheduled.Cancel(); !ok {
		t.Fatal("Cancel() = false, want true")
	}

	select {
	case <-received:
		t.Fatal("message delivered after cancel")
	case <-time.After(200 * time.Millisecond):
	}

	select {
	case doneErr := <-scheduled.Done():
		if !errors.Is(doneErr, ErrScheduleCanceled) {
			t.Fatalf("scheduled result error = %v, want %v", doneErr, ErrScheduleCanceled)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive scheduler completion")
	}
}

func TestContextTellAfterSetsSender(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	fromCh := make(chan ActorID, 1)
	target, err := sys.Spawn(context.Background(), "target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				if s := ctx.Sender(); s != nil {
					fromCh <- s.ID()
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	schedulerRef, err := sys.Spawn(context.Background(), "scheduler", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				_, callErr := ctx.TellAfter(ctx, 10*time.Millisecond, target, "ping")
				return callErr
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(scheduler) error = %v", err)
	}

	if err := sys.Tell(context.Background(), schedulerRef, "go"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case got := <-fromCh:
		if got != schedulerRef.ID() {
			t.Fatalf("sender actor = %q, want %q", got, schedulerRef.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive scheduled sender id")
	}
}
