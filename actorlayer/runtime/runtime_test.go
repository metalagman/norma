package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/normahq/norma/actorlayer/dispatch"
	actorengine "github.com/normahq/norma/actorlayer/engine"
)

type testEnvelope struct {
	to string
}

type testActor struct {
	address string
	err     error
	calls   int
}

func (a *testActor) Address() string { return a.address }
func (a *testActor) Handle(_ context.Context, _ any) error {
	a.calls++
	return a.err
}

type testDelivery struct {
	env        any
	attempt    int
	max        int
	acked      bool
	retried    bool
	deadletter bool
}

func (d *testDelivery) Envelope() any { return d.env }
func (d *testDelivery) Attempt() int {
	if d.attempt <= 0 {
		return 1
	}
	return d.attempt
}
func (d *testDelivery) MaxAttempts() int               { return d.max }
func (*testDelivery) InProgress(context.Context) error { return nil }
func (d *testDelivery) Ack(context.Context) error {
	d.acked = true
	return nil
}
func (d *testDelivery) Retry(context.Context, time.Duration, string) error {
	d.retried = true
	return nil
}
func (d *testDelivery) DeadLetter(context.Context, string) error {
	d.deadletter = true
	return nil
}

func TestRuntimeHandleAckOnSuccess(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	actor := &testActor{address: "session:*"}
	if err := registry.Register(actor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := New(Config{
		Registry: registry,
		AddressOf: func(envelope any) (string, error) {
			v, ok := envelope.(testEnvelope)
			if !ok {
				return "", errors.New("bad envelope")
			}
			return v.to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{env: testEnvelope{to: "session:s-1"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.acked {
		t.Fatal("Ack() was not called")
	}
	if actor.calls != 1 {
		t.Fatalf("actor calls = %d, want 1", actor.calls)
	}
}

func TestRuntimeHandleDeadlettersOnResolveFailure(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	rt, err := New(Config{
		Registry: registry,
		AddressOf: func(envelope any) (string, error) {
			return envelope.(testEnvelope).to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{env: testEnvelope{to: "session:s-404"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.deadletter {
		t.Fatal("DeadLetter() was not called")
	}
}

func TestRuntimeHandleRetriesOnActorError(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	if err := registry.Register(&testActor{address: "session:*", err: errors.New("temporary")}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := New(Config{
		Registry: registry,
		AddressOf: func(envelope any) (string, error) {
			return envelope.(testEnvelope).to, nil
		},
		Retry: retryAlways(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{env: testEnvelope{to: "session:s-1"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.retried {
		t.Fatal("Retry() was not called")
	}
}

func retryNever() actorengine.RetryPolicy {
	return actorengine.RetryPolicy{IsRetryable: func(error) bool { return false }}
}

func retryAlways() actorengine.RetryPolicy {
	return actorengine.RetryPolicy{
		IsRetryable: func(error) bool { return true },
		Backoff:     func(int) time.Duration { return time.Millisecond },
	}
}
