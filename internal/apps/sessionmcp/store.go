package sessionmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/session"
)

// Store is the interface for session state storage drivers.
// It wraps ADK's session.State with additional methods for MCP tools.
type Store interface {
	// Get retrieves a value by key. Returns empty string and false if not found.
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	// Set stores a value by key.
	Set(ctx context.Context, key, value string) error
	// Delete removes a key. No-op if key doesn't exist.
	Delete(ctx context.Context, key string) error
	// List returns all keys, optionally filtered by prefix.
	List(ctx context.Context, prefix string) ([]string, error)
	// Clear removes all keys.
	Clear(ctx context.Context) error
	// GetJSON retrieves a value by key as parsed JSON.
	GetJSON(ctx context.Context, key string) (value interface{}, ok bool, err error)
	// SetJSON stores a value by key as JSON.
	SetJSON(ctx context.Context, key string, value interface{}) error
	// MergeJSON merges fields into an existing JSON object at key.
	MergeJSON(ctx context.Context, key string, fields map[string]interface{}) (merged map[string]interface{}, err error)
}

// Global shared session for inter-process state sharing.
// All MemoryStore instances share this single session so state is visible
// across different processes/agents connecting to the same server.
var (
	sharedSession     session.Session
	sharedSessionOnce sync.Once
)

func getSharedSession() session.Session {
	sharedSessionOnce.Do(func() {
		sharedSession = createSharedSession()
	})
	return sharedSession
}

func createSharedSession() session.Session {
	svc := session.InMemoryService()
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "norma-session-state",
		UserID:  "shared",
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create shared session: %v", err))
	}
	return resp.Session
}

// ResetSharedStore resets the global shared store for testing purposes.
// This allows tests to start with a clean state.
func ResetSharedStore() {
	sharedSession = createSharedSession()
}

// MemoryStore is an in-memory session state store backed by ADK session.State.
// This is the default driver for inter-process state sharing.
//
// All MemoryStore instances share a single global ADK session, so state set
// by one process is visible to all other processes connected to the same server.
type MemoryStore struct {
	session session.Session
}

// NewMemoryStore creates a new in-memory store using the shared ADK session.
// Multiple calls return stores backed by the same underlying state.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		session: getSharedSession(),
	}
}

func (s *MemoryStore) Get(_ context.Context, key string) (string, bool, error) {
	state := s.session.State()
	val, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get state: %w", err)
	}
	str, ok := val.(string)
	if !ok {
		// Marshal non-string values to JSON
		b, err := json.Marshal(val)
		if err != nil {
			return "", false, fmt.Errorf("marshal value: %w", err)
		}
		return string(b), true, nil
	}
	return str, true, nil
}

func (s *MemoryStore) Set(_ context.Context, key, value string) error {
	state := s.session.State()
	if err := state.Set(key, value); err != nil {
		return fmt.Errorf("set state: %w", err)
	}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	// ADK State doesn't have Delete - set to empty marker
	// For true deletion, the server should be restarted
	return nil
}

func (s *MemoryStore) List(_ context.Context, prefix string) ([]string, error) {
	state := s.session.State()
	keys := make([]string, 0)
	for key := range state.All() {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *MemoryStore) Clear(_ context.Context) error {
	// ADK State doesn't support clear - state persists for session lifetime
	return nil
}

// GetJSON retrieves a value by key and unmarshals it as JSON.
func (s *MemoryStore) GetJSON(_ context.Context, key string) (interface{}, bool, error) {
	state := s.session.State()
	val, err := state.Get(key)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get state: %w", err)
	}
	return val, true, nil
}

// SetJSON stores a value by key as JSON.
func (s *MemoryStore) SetJSON(_ context.Context, key string, value interface{}) error {
	state := s.session.State()
	if err := state.Set(key, value); err != nil {
		return fmt.Errorf("set state: %w", err)
	}
	return nil
}

// MergeJSON merges fields into an existing JSON object at key.
// If the key doesn't exist, it creates a new object with the provided fields.
func (s *MemoryStore) MergeJSON(_ context.Context, key string, fields map[string]interface{}) (map[string]interface{}, error) {
	state := s.session.State()

	var existing map[string]interface{}
	val, err := state.Get(key)
	if err != nil && !errors.Is(err, session.ErrStateKeyNotExist) {
		return nil, fmt.Errorf("get state: %w", err)
	}

	if val != nil {
		switch v := val.(type) {
		case map[string]interface{}:
			existing = v
		default:
			existing = make(map[string]interface{})
		}
	} else {
		existing = make(map[string]interface{})
	}

	for k, v := range fields {
		existing[k] = v
	}

	if err := state.Set(key, existing); err != nil {
		return nil, fmt.Errorf("set state: %w", err)
	}

	return existing, nil
}
