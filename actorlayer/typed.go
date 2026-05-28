package actorlayer

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrMessageTypeMismatch is returned when an envelope payload does not match
	// the typed actor message contract.
	ErrMessageTypeMismatch = errors.New("actorlayer: message type mismatch")
	// ErrResponseTypeMismatch is returned when AskTyped receives a response
	// payload that does not match the requested response type.
	ErrResponseTypeMismatch = errors.New("actorlayer: response type mismatch")
)

// EnvelopeMeta carries transport/runtime metadata for a typed envelope.
type EnvelopeMeta struct {
	ID            MessageID
	CorrelationID CorrelationID

	To      Ref
	From    Ref
	ReplyTo *Ref

	Headers  map[string]string
	Deadline time.Time
	SentAt   time.Time
}

// TypedEnvelope is the typed message envelope presented to Actor[M].
type TypedEnvelope[M any] struct {
	Meta    EnvelopeMeta
	Message M
}

// Actor is a typed actor contract for one message type M.
type Actor[M any] interface {
	Receive(ctx Context, env TypedEnvelope[M]) error
}

// ActorFunc adapts a function into Actor[M].
type ActorFunc[M any] func(ctx Context, env TypedEnvelope[M]) error

// Receive executes the wrapped function.
func (f ActorFunc[M]) Receive(ctx Context, env TypedEnvelope[M]) error {
	return f(ctx, env)
}

// ActorRef is a typed reference to an actor expecting message type M.
type ActorRef[M any] struct {
	ref Ref
}

// ID returns the actor identifier.
func (r ActorRef[M]) ID() ActorID {
	return r.ref.ID()
}

// Untyped returns the underlying untyped reference.
func (r ActorRef[M]) Untyped() Ref {
	return r.ref
}

// Tell sends a typed message to the actor.
func (r ActorRef[M]) Tell(ctx context.Context, msg M, opts ...TellOption) error {
	return r.ref.Tell(ctx, msg, opts...)
}

// TypedProps configures a typed actor spawn.
type TypedProps[M any] struct {
	Kind     string
	NewActor func(ctx SpawnContext) (Actor[M], error)

	Mailbox    MailboxFactory
	Supervisor SupervisorStrategy
}

// SpawnTyped creates and starts a typed actor instance.
func SpawnTyped[M any](ctx context.Context, sys *System, name string, props TypedProps[M], opts ...SpawnOption) (ActorRef[M], error) {
	if sys == nil {
		return ActorRef[M]{}, errors.New("actorlayer: system is nil")
	}
	if props.NewActor == nil {
		return ActorRef[M]{}, errors.New("actorlayer: typed props.NewActor is required")
	}

	ref, err := sys.Spawn(ctx, name, Props{
		Kind: props.Kind,
		NewBehavior: func(spawn SpawnContext) (Behavior, error) {
			typedActor, createErr := props.NewActor(spawn)
			if createErr != nil {
				return nil, createErr
			}
			return ReceiveFunc(func(actx Context, env Envelope) error {
				msg, ok := payloadAs[M](env.Payload)
				if !ok {
					return fmt.Errorf(
						"%w: actor=%s expected=%T actual=%T",
						ErrMessageTypeMismatch,
						spawn.Self.ID(),
						*new(M),
						env.Payload,
					)
				}
				return typedActor.Receive(actx, TypedEnvelope[M]{
					Meta:    envelopeMetaFrom(env),
					Message: msg,
				})
			}), nil
		},
		Mailbox:    props.Mailbox,
		Supervisor: props.Supervisor,
	}, opts...)
	if err != nil {
		return ActorRef[M]{}, err
	}
	return ActorRef[M]{ref: ref}, nil
}

// AskTyped sends a typed request and expects a typed response payload.
func AskTyped[Req any, Resp any](
	ctx context.Context,
	sys *System,
	to ActorRef[Req],
	msg Req,
	opts ...AskOption,
) (TypedEnvelope[Resp], error) {
	respEnv, err := Ask(ctx, sys, to.ref, msg, opts...)
	if err != nil {
		return TypedEnvelope[Resp]{}, err
	}
	respPayload, ok := payloadAs[Resp](respEnv.Payload)
	if !ok {
		return TypedEnvelope[Resp]{}, fmt.Errorf(
			"%w: actor=%s expected=%T actual=%T",
			ErrResponseTypeMismatch,
			to.ref.ID(),
			*new(Resp),
			respEnv.Payload,
		)
	}
	return TypedEnvelope[Resp]{
		Meta:    envelopeMetaFrom(respEnv),
		Message: respPayload,
	}, nil
}

// EventActor is a semantic actor type for one-way event handling.
type EventActor[M any] interface {
	OnEvent(ctx Context, env TypedEnvelope[M]) error
}

// EventActorFunc adapts a function into EventActor[M].
type EventActorFunc[M any] func(ctx Context, env TypedEnvelope[M]) error

// OnEvent executes the wrapped function.
func (f EventActorFunc[M]) OnEvent(ctx Context, env TypedEnvelope[M]) error {
	return f(ctx, env)
}

// AdaptEventActor converts an EventActor into a generic Actor.
func AdaptEventActor[M any](eventActor EventActor[M]) Actor[M] {
	return ActorFunc[M](func(ctx Context, env TypedEnvelope[M]) error {
		return eventActor.OnEvent(ctx, env)
	})
}

// RequestActor is a semantic actor type for request-response workflows.
type RequestActor[Req any, Resp any] interface {
	Handle(ctx Context, env TypedEnvelope[Req]) (Resp, error)
}

// RequestActorFunc adapts a function into RequestActor[Req, Resp].
type RequestActorFunc[Req any, Resp any] func(ctx Context, env TypedEnvelope[Req]) (Resp, error)

// Handle executes the wrapped function.
func (f RequestActorFunc[Req, Resp]) Handle(ctx Context, env TypedEnvelope[Req]) (Resp, error) {
	return f(ctx, env)
}

// AdaptRequestActor converts RequestActor into Actor[Req] that replies to
// EnvelopeMeta.ReplyTo with the typed response when reply target is present.
func AdaptRequestActor[Req any, Resp any](requestActor RequestActor[Req, Resp]) Actor[Req] {
	return ActorFunc[Req](func(ctx Context, env TypedEnvelope[Req]) error {
		resp, err := requestActor.Handle(ctx, env)
		if err != nil {
			return err
		}
		if env.Meta.ReplyTo == nil {
			return nil
		}
		return ctx.Tell(ctx, *env.Meta.ReplyTo, resp, WithCorrelationID(env.Meta.CorrelationID))
	})
}

func envelopeMetaFrom(env Envelope) EnvelopeMeta {
	return EnvelopeMeta{
		ID:            env.ID,
		CorrelationID: env.CorrelationID,
		To:            env.To,
		From:          env.From,
		ReplyTo:       env.ReplyTo,
		Headers:       cloneHeaders(env.Headers),
		Deadline:      env.Deadline,
		SentAt:        env.SentAt,
	}
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func payloadAs[T any](payload any) (T, bool) {
	var zero T
	if payload == nil {
		return zero, any(zero) == nil
	}
	msg, ok := payload.(T)
	if !ok {
		return zero, false
	}
	return msg, true
}
