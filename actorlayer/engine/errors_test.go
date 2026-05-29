package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/normahq/norma/actorlayer/dispatch"
)

func TestResolveError(t *testing.T) {
	t.Parallel()

	err := &ResolveError{Address: "session:404"}
	if got := err.Error(); got != "actor not found: session:404" {
		t.Fatalf("err.Error() = %q, want %q", got, "actor not found: session:404")
	}
	if !errors.Is(err, ErrActorNotFound) {
		t.Fatal("errors.Is(err, ErrActorNotFound) = false, want true")
	}
}

func TestDispatchRuntimeHandleDeliveryReturnsResolveError(t *testing.T) {
	t.Parallel()

	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: dispatch.NewMemoryRegistry(),
		AddressOf: func(env any) (string, error) {
			return "session:missing", nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}

	gotErr := rt.handleDelivery(context.Background(), &testRuntimeDelivery{env: envelope{to: "session:missing"}})
	if gotErr == nil {
		t.Fatal("handleDelivery() = nil, want resolve error")
	}
	var resolveErr *ResolveError
	if !errors.As(gotErr, &resolveErr) {
		t.Fatalf("handleDelivery() error type = %T, want *ResolveError", gotErr)
	}
	if resolveErr.Address != "session:missing" {
		t.Fatalf("resolveErr.Address = %q, want %q", resolveErr.Address, "session:missing")
	}
}
