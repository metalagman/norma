package actorlayer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSpawnTypedTellDeliversTypedMessage(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan TypedEnvelope[int], 1)
	ref, err := SpawnTyped[int](context.Background(), sys, "typed-int", TypedProps[int]{
		NewActor: func(SpawnContext) (Actor[int], error) {
			return ActorFunc[int](func(_ Context, env TypedEnvelope[int]) error {
				received <- env
				return nil
			}), nil
		},
	}, WithActorID("typed-int"))
	if err != nil {
		t.Fatalf("SpawnTyped() error = %v", err)
	}

	const corrID = CorrelationID("corr-1")
	if err := ref.Tell(context.Background(), 42, WithCorrelationID(corrID), WithHeader("k", "v")); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case env := <-received:
		if env.Message != 42 {
			t.Fatalf("message = %d, want 42", env.Message)
		}
		if env.Meta.CorrelationID != corrID {
			t.Fatalf("correlation = %q, want %q", env.Meta.CorrelationID, corrID)
		}
		if got := env.Meta.Headers["k"]; got != "v" {
			t.Fatalf("header k = %q, want v", got)
		}
		if env.Meta.To.ID() != ref.ID() {
			t.Fatalf("to actor id = %q, want %q", env.Meta.To.ID(), ref.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for typed message")
	}
}

func TestSpawnTypedRejectsPayloadTypeMismatch(t *testing.T) {
	t.Parallel()

	sup := &recordingSupervisor{directive: Stop, decided: make(chan Failure, 1)}
	sys, err := NewSystem(Config{Supervision: sup})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	var handled atomic.Int32
	ref, err := SpawnTyped[int](context.Background(), sys, "typed-int", TypedProps[int]{
		NewActor: func(SpawnContext) (Actor[int], error) {
			return ActorFunc[int](func(_ Context, _ TypedEnvelope[int]) error {
				handled.Add(1)
				return nil
			}), nil
		},
	}, WithActorID("typed-int-mismatch"))
	if err != nil {
		t.Fatalf("SpawnTyped() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref.Untyped(), "not-an-int"); err != nil {
		t.Fatalf("Tell(untyped) error = %v", err)
	}

	select {
	case failure := <-sup.decided:
		if !errors.Is(failure.Err, ErrMessageTypeMismatch) {
			t.Fatalf("failure err = %v, want %v", failure.Err, ErrMessageTypeMismatch)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected supervisor failure notification")
	}
	if got := handled.Load(); got != 0 {
		t.Fatalf("typed actor handled %d messages, want 0", got)
	}
}

func TestAskTypedSuccessAndResponseTypeMismatch(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	ref, err := SpawnTyped[string](context.Background(), sys, "typed-req", TypedProps[string]{
		NewActor: func(SpawnContext) (Actor[string], error) {
			return AdaptRequestActor[string, int](RequestActorFunc[string, int](func(_ Context, env TypedEnvelope[string]) (int, error) {
				return len(env.Message), nil
			})), nil
		},
	}, WithActorID("typed-ask"))
	if err != nil {
		t.Fatalf("SpawnTyped() error = %v", err)
	}

	okResp, err := AskTyped[string, int](context.Background(), sys, ref, "hello", WithTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatalf("AskTyped() error = %v", err)
	}
	if okResp.Message != 5 {
		t.Fatalf("response = %d, want 5", okResp.Message)
	}

	_, err = AskTyped[string, string](context.Background(), sys, ref, "hello", WithTimeout(500*time.Millisecond))
	if !errors.Is(err, ErrResponseTypeMismatch) {
		t.Fatalf("AskTyped() mismatch error = %v, want %v", err, ErrResponseTypeMismatch)
	}
}

func TestAdaptEventActorPassesThrough(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan string, 1)
	ref, err := SpawnTyped[string](context.Background(), sys, "typed-event", TypedProps[string]{
		NewActor: func(SpawnContext) (Actor[string], error) {
			eventActor := EventActorFunc[string](func(_ Context, env TypedEnvelope[string]) error {
				received <- env.Message
				return nil
			})
			return AdaptEventActor[string](eventActor), nil
		},
	}, WithActorID("typed-event"))
	if err != nil {
		t.Fatalf("SpawnTyped() error = %v", err)
	}

	if err := ref.Tell(context.Background(), "ping"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case msg := <-received:
		if msg != "ping" {
			t.Fatalf("event message = %q, want ping", msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event actor message")
	}
}
