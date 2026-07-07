package adk

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/normahq/norma/v2/pkg/actorlayer"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestToolsetExposesSendAndAsk(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	ts, err := Toolset(sys)
	if err != nil {
		t.Fatalf("Toolset() error = %v", err)
	}
	tools, err := ts.Tools(nil)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	if !names[toolNameActorSend] || !names[toolNameActorAsk] {
		t.Fatalf("tools names = %+v, want %q and %q", names, toolNameActorSend, toolNameActorAsk)
	}
}

func TestToolsetExposesPublishWhenEnabled(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	ts, err := Toolset(sys, WithPublishEnabled(true))
	if err != nil {
		t.Fatalf("Toolset() error = %v", err)
	}
	tools, err := ts.Tools(nil)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("tools count = %d, want 3", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	if !names[toolNameActorPublish] {
		t.Fatalf("tools names = %+v, want %q", names, toolNameActorPublish)
	}
}

func TestActorSendAllowedActor(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan any, 1)
	receiver, err := sys.Spawn(context.Background(), "receiver", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, env actorlayer.Envelope) error {
				received <- env.Payload
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(receiver) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{receiver.ID(): {}}},
			DefaultAskTimeout: defaultAskTimeout,
			MaxAskTimeout:     defaultAskTimeout,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{"worker": receiver},
		},
	}

	err = svc.send(minReadonlyContext{}, actorSendInput{
		To:      "worker",
		Topic:   "tasks.run",
		Payload: map[string]any{"id": 7},
	})
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}

	select {
	case payload := <-received:
		msg, ok := payload.(Message)
		if !ok {
			t.Fatalf("payload type = %T, want Message", payload)
		}
		if msg.Topic != "tasks.run" {
			t.Fatalf("topic = %q, want %q", msg.Topic, "tasks.run")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive sent message")
	}
}

func TestActorSendDisallowedTarget(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	receiver, err := sys.Spawn(context.Background(), "receiver", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, _ actorlayer.Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(receiver) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{}},
			DefaultAskTimeout: defaultAskTimeout,
			MaxAskTimeout:     defaultAskTimeout,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{"worker": receiver},
		},
	}

	err = svc.send(minReadonlyContext{}, actorSendInput{To: "worker", Payload: "hello"})
	if err == nil {
		t.Fatal("expected disallowed target error, got nil")
	}
}

func TestActorAskReturnsReplyPayload(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	replier, err := sys.Spawn(context.Background(), "replier", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(ctx actorlayer.Context, env actorlayer.Envelope) error {
				if env.ReplyTo != nil {
					return ctx.Tell(ctx, *env.ReplyTo, "pong")
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(replier) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{replier.ID(): {}}},
			DefaultAskTimeout: 250 * time.Millisecond,
			MaxAskTimeout:     1 * time.Second,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{"worker": replier},
		},
	}

	reply, err := svc.ask(minReadonlyContext{}, actorAskInput{To: "worker", Payload: "ping", TimeoutMS: 200})
	if err != nil {
		t.Fatalf("ask() error = %v", err)
	}
	if got, ok := reply.Payload.(string); !ok || got != "pong" {
		t.Fatalf("reply = %#v, want pong", reply.Payload)
	}
}

func TestActorAskTimeoutReturnsError(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	slow, err := sys.Spawn(context.Background(), "slow", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, _ actorlayer.Envelope) error {
				time.Sleep(300 * time.Millisecond)
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(slow) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{slow.ID(): {}}},
			DefaultAskTimeout: 50 * time.Millisecond,
			MaxAskTimeout:     100 * time.Millisecond,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{"worker": slow},
		},
	}

	_, err = svc.ask(minReadonlyContext{}, actorAskInput{To: "worker", Payload: "ping", TimeoutMS: 50})
	if !errors.Is(err, actorlayer.ErrAskTimeout) {
		t.Fatalf("ask() error = %v, want %v", err, actorlayer.ErrAskTimeout)
	}
}

func TestPayloadLimitRejected(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	receiver, err := sys.Spawn(context.Background(), "receiver", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, _ actorlayer.Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(receiver) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{receiver.ID(): {}}},
			DefaultAskTimeout: defaultAskTimeout,
			MaxAskTimeout:     defaultAskTimeout,
			MaxPayloadBytes:   8,
			NamedRefs:         map[string]actorlayer.Ref{"worker": receiver},
		},
	}

	err = svc.send(minReadonlyContext{}, actorSendInput{To: "worker", Payload: map[string]any{"large": "payload"}})
	if err == nil {
		t.Fatal("expected payload limit error, got nil")
	}
}

func TestAskTimeoutExceedsMaxRejected(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	receiver, err := sys.Spawn(context.Background(), "receiver", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, _ actorlayer.Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(receiver) error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           namedOnlyAddressPolicy{allowed: map[actorlayer.ActorID]struct{}{receiver.ID(): {}}},
			DefaultAskTimeout: 100 * time.Millisecond,
			MaxAskTimeout:     100 * time.Millisecond,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{"worker": receiver},
		},
	}

	_, err = svc.ask(minReadonlyContext{}, actorAskInput{To: "worker", Payload: "ping", TimeoutMS: 500})
	if err == nil {
		t.Fatal("expected max-timeout error, got nil")
	}
}

func TestActorPublishDeliversToSubscribers(t *testing.T) {
	t.Parallel()

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan any, 1)
	sub, err := sys.Spawn(context.Background(), "sub", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, env actorlayer.Envelope) error {
				received <- env.Payload
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(sub) error = %v", err)
	}
	if err := sys.Subscribe("topic.1", sub); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	svc := &toolsetService{
		sys: sys,
		cfg: ToolsetConfig{
			Allowed:           allowAllPolicy{},
			DefaultAskTimeout: defaultAskTimeout,
			MaxAskTimeout:     defaultAskTimeout,
			MaxPayloadBytes:   defaultMaxPayload,
			NamedRefs:         map[string]actorlayer.Ref{},
			EnablePublish:     true,
		},
	}

	if err := svc.publish(minReadonlyContext{}, actorPublishInput{Topic: "topic.1", Payload: map[string]any{"k": "v"}}); err != nil {
		t.Fatalf("publish() error = %v", err)
	}

	select {
	case got := <-received:
		msg, ok := got.(actorlayer.PublishedMessage)
		if !ok {
			t.Fatalf("payload type = %T, want actorlayer.PublishedMessage", got)
		}
		if msg.Topic != "topic.1" {
			t.Fatalf("topic = %q, want %q", msg.Topic, "topic.1")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive published event")
	}
}

type allowAllPolicy struct{}

func (allowAllPolicy) Allowed(agent.ReadonlyContext, actorlayer.ActorID, string) bool {
	return true
}

// minReadonlyContext is the minimal agent.ReadonlyContext implementation
// needed for policy checks in unit tests.
type minReadonlyContext struct{}

func (minReadonlyContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (minReadonlyContext) Done() <-chan struct{}       { return nil }
func (minReadonlyContext) Err() error                  { return nil }
func (minReadonlyContext) Value(any) any               { return nil }
func (minReadonlyContext) UserContent() *genai.Content { return nil }
func (minReadonlyContext) InvocationID() string        { return "" }
func (minReadonlyContext) AgentName() string           { return "" }
func (minReadonlyContext) ReadonlyState() session.ReadonlyState {
	return nil
}
func (minReadonlyContext) UserID() string    { return "" }
func (minReadonlyContext) AppName() string   { return "" }
func (minReadonlyContext) SessionID() string { return "" }
func (minReadonlyContext) Branch() string    { return "" }

// Ensure the iter package import remains used when this file changes around callbacks.
var _ = iter.Seq2[string, any](nil)
