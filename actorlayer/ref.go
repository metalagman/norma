package actorlayer

import "context"

// ActorID is a stable local identifier for an actor within one system.
type ActorID string

// Ref is an addressable reference to an actor.
type Ref struct {
	id  ActorID
	sys *System
}

// ID returns the actor identifier.
func (r Ref) ID() ActorID {
	return r.id
}

// Tell sends an asynchronous message to the actor.
func (r Ref) Tell(ctx context.Context, payload any, opts ...TellOption) error {
	if r.sys == nil {
		return ErrActorNotFound
	}
	return r.sys.Tell(ctx, r, payload, opts...)
}

// Ask sends a request and waits for a reply.
func (r Ref) Ask(ctx context.Context, payload any, opts ...AskOption) (Envelope, error) {
	if r.sys == nil {
		return Envelope{}, ErrActorNotFound
	}
	return Ask(ctx, r.sys, r, payload, opts...)
}

func (r Ref) validFor(sys *System) bool {
	return r.sys == sys && r.id != ""
}
