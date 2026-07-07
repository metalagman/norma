package engine

import (
	"context"
	"errors"
	"testing"

	actorlayer "github.com/normahq/norma/v2/pkg/actorlayer"
	"github.com/normahq/norma/v2/pkg/actorlayer/dispatch"
)

func TestResolveError(t *testing.T) {
	t.Parallel()

	err := &ResolveError{Address: "session:404"}
	want := actorlayer.ErrActorNotFound.Error() + ": session:404"
	if got := err.Error(); got != want {
		t.Fatalf("err.Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, actorlayer.ErrActorNotFound) {
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
