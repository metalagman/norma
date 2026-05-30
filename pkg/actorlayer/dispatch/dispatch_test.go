package dispatch

import (
	"context"
	"strings"
	"testing"
)

type testActor struct {
	address string
	err     error
}

func (a testActor) Address() string { return a.address }
func (a testActor) Handle(context.Context, any) error {
	return a.err
}

func TestMemoryRegistryResolveExact(t *testing.T) {
	registry := NewMemoryRegistry()
	if err := registry.Register(testActor{address: "session:s-1"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	actor, ok := registry.Resolve("session:s-1")
	if !ok {
		t.Fatal("Resolve() found = false, want true")
	}
	if err := actor.Handle(context.Background(), "payload"); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func TestMemoryRegistryResolveWildcard(t *testing.T) {
	registry := NewMemoryRegistry()
	if err := registry.Register(testActor{address: "session:*"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, ok := registry.Resolve("session:s-9"); !ok {
		t.Fatal("Resolve() found = false, want true")
	}
}

func TestMemoryRegistryRegisterRejectsEmptyAddress(t *testing.T) {
	registry := NewMemoryRegistry()
	err := registry.Register(testActor{address: "   "})
	if err == nil {
		t.Fatal("Register() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "actor address is required") {
		t.Fatalf("Register() error = %v, want actor address message", err)
	}
}
