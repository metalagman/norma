package actorlayer

import (
	"context"
	"sync"
	"time"
)

// State stores actor-private mutable key/value data.
type State interface {
	Get(key string) (any, bool)
	Set(key string, value any)
	Delete(key string)
}

// Context provides actor-local execution context for message handling.
type Context interface {
	context.Context

	Self() Ref
	Parent() *Ref
	Sender() *Ref

	Tell(ctx context.Context, to Ref, payload any, opts ...TellOption) error
	TellAfter(ctx context.Context, delay time.Duration, to Ref, payload any, opts ...TellOption) (Scheduled, error)
	Ask(ctx context.Context, to Ref, payload any, opts ...AskOption) (Envelope, error)

	Spawn(ctx context.Context, name string, props Props, opts ...SpawnOption) (Ref, error)
	Stop(ctx context.Context, ref Ref) error
	Watch(ctx context.Context, ref Ref) error

	State() State
	Publish(ctx context.Context, topic string, payload any) error
}

type actorContext struct {
	context.Context

	sys    *System
	self   Ref
	parent *Ref
	sender *Ref
	state  *stateMap
}

func (c *actorContext) Self() Ref {
	return c.self
}

func (c *actorContext) Parent() *Ref {
	return c.parent
}

func (c *actorContext) Sender() *Ref {
	return c.sender
}

func (c *actorContext) Tell(ctx context.Context, to Ref, payload any, opts ...TellOption) error {
	opts = append(opts, WithFrom(c.self))
	return c.sys.Tell(ctx, to, payload, opts...)
}

func (c *actorContext) TellAfter(ctx context.Context, delay time.Duration, to Ref, payload any, opts ...TellOption) (Scheduled, error) {
	opts = append(opts, WithFrom(c.self))
	return c.sys.TellAfter(ctx, delay, to, payload, opts...)
}

func (c *actorContext) Ask(ctx context.Context, to Ref, payload any, opts ...AskOption) (Envelope, error) {
	opts = append(opts, WithAskFrom(c.self))
	return Ask(ctx, c.sys, to, payload, opts...)
}

func (c *actorContext) Spawn(ctx context.Context, name string, props Props, opts ...SpawnOption) (Ref, error) {
	return c.sys.spawnInternal(ctx, name, props, &c.self, opts...)
}

func (c *actorContext) Stop(ctx context.Context, ref Ref) error {
	return c.sys.Stop(ctx, ref)
}

func (c *actorContext) Watch(ctx context.Context, ref Ref) error {
	return c.sys.Watch(ctx, c.self, ref)
}

func (c *actorContext) State() State {
	return c.state
}

func (c *actorContext) Publish(ctx context.Context, topic string, payload any) error {
	return c.sys.Publish(ctx, topic, payload)
}

type stateMap struct {
	mu sync.RWMutex
	m  map[string]any
}

func newStateMap() *stateMap {
	return &stateMap{m: make(map[string]any)}
}

func (s *stateMap) Get(key string) (any, bool) {
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()
	return v, ok
}

func (s *stateMap) Set(key string, value any) {
	s.mu.Lock()
	s.m[key] = value
	s.mu.Unlock()
}

func (s *stateMap) Delete(key string) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
}
