package actorlayer

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextMethodsAndLifecycleHelpers(t *testing.T) {
	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	errCh := make(chan error, 8)
	childParentChecked := make(chan struct{}, 1)
	terminatedCh := make(chan Terminated, 1)

	controller, err := sys.Spawn(context.Background(), "controller", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, env Envelope) error {
				switch msg := env.Payload.(type) {
				case string:
					if msg != "run" {
						return nil
					}
					if ctx.Self().ID() == "" {
						errCh <- errors.New("ctx.Self() returned empty id")
					}
					if ctx.Parent() != nil {
						errCh <- errors.New("top-level actor should have nil parent")
					}

					state := ctx.State()
					state.Set("k", "v")
					if got, ok := state.Get("k"); !ok || got != "v" {
						errCh <- fmt.Errorf("state.Get(k) = (%v,%v), want (v,true)", got, ok)
					}
					state.Delete("k")
					if _, ok := state.Get("k"); ok {
						errCh <- errors.New("state.Delete(k) did not remove key")
					}

					child, spawnErr := ctx.Spawn(ctx, "child", Props{
						NewBehavior: func(SpawnContext) (Behavior, error) {
							return ReceiveFunc(func(c Context, childEnv Envelope) error {
								if childEnv.Payload == "check-parent" {
									if c.Parent() == nil {
										errCh <- errors.New("child parent should not be nil")
									} else {
										childParentChecked <- struct{}{}
									}
								}
								return nil
							}), nil
						},
					})
					if spawnErr != nil {
						return spawnErr
					}

					if tellErr := ctx.Tell(ctx, child, "check-parent"); tellErr != nil {
						return tellErr
					}
					if watchErr := ctx.Watch(ctx, child); watchErr != nil {
						return watchErr
					}
					return ctx.Stop(ctx, child)
				case Terminated:
					terminatedCh <- msg
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(controller) error = %v", err)
	}

	if err := sys.Tell(context.Background(), controller, "run"); err != nil {
		t.Fatalf("Tell(run) error = %v", err)
	}

	select {
	case <-childParentChecked:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for child parent check")
	}

	select {
	case msg := <-terminatedCh:
		if msg.ActorID == "" {
			t.Fatal("terminated actor id is empty")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for terminated message")
	}

	select {
	case got := <-errCh:
		t.Fatalf("context assertion error: %v", got)
	default:
	}
}

func TestRefAskAndMetadataOptions(t *testing.T) {
	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	tellEnvCh := make(chan Envelope, 1)
	askEnvCh := make(chan Envelope, 1)

	ref, err := sys.Spawn(context.Background(), "meta", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, env Envelope) error {
				switch env.Payload {
				case "tell":
					tellEnvCh <- env
				case "ask":
					askEnvCh <- env
					if env.ReplyTo != nil {
						return ctx.Tell(ctx, *env.ReplyTo, "ok")
					}
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Minute).Round(0)
	msgID := MessageID("msg-custom")
	if err := ref.Tell(context.Background(), "tell",
		WithHeaders(map[string]string{"h1": "v1", "h2": "v2"}),
		WithMessageID(msgID),
		WithDeadline(deadline),
	); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case env := <-tellEnvCh:
		if env.ID != msgID {
			t.Fatalf("tell message id = %q, want %q", env.ID, msgID)
		}
		if !env.Deadline.Equal(deadline) {
			t.Fatalf("tell deadline = %v, want %v", env.Deadline, deadline)
		}
		if env.Headers["h1"] != "v1" || env.Headers["h2"] != "v2" {
			t.Fatalf("tell headers = %#v", env.Headers)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for tell envelope")
	}

	resp, err := ref.Ask(context.Background(), "ask",
		WithAskHeader("x", "1"),
		WithAskHeaders(map[string]string{"y": "2"}),
		WithAskCorrelationID("cid-1"),
		WithTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("Ref.Ask() error = %v", err)
	}
	if got := resp.Payload; got != "ok" {
		t.Fatalf("Ref.Ask() payload = %v, want ok", got)
	}

	select {
	case env := <-askEnvCh:
		if env.CorrelationID != "cid-1" {
			t.Fatalf("ask correlation id = %q, want cid-1", env.CorrelationID)
		}
		if env.Headers["x"] != "1" || env.Headers["y"] != "2" {
			t.Fatalf("ask headers = %#v", env.Headers)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ask envelope")
	}
}

func TestUnsubscribeStopsTopicDelivery(t *testing.T) {
	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan string, 2)
	subscriber, err := sys.Spawn(context.Background(), "subscriber", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				pub, ok := env.Payload.(PublishedMessage)
				if !ok {
					return nil
				}
				if text, textOK := pub.Payload.(string); textOK {
					received <- text
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Subscribe("topic", subscriber); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err := sys.Publish(context.Background(), "topic", "first"); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	select {
	case got := <-received:
		if got != "first" {
			t.Fatalf("first payload = %q, want first", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for first publish")
	}

	if err := sys.Unsubscribe("topic", subscriber); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
	if err := sys.Publish(context.Background(), "topic", "second"); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	select {
	case got := <-received:
		t.Fatalf("unexpected publish after unsubscribe: %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRestartDirectiveRebuildsBehavior(t *testing.T) {
	obs := &StatsObserver{}
	sup := &recordingSupervisor{directive: Restart, decided: make(chan Failure, 2)}
	sys, err := NewSystem(Config{Supervision: sup, Observer: obs})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	failHit := make(chan struct{}, 1)
	restartedHandled := make(chan struct{}, 1)
	var behaviorBuilds atomic.Int32

	ref, err := sys.Spawn(context.Background(), "restartable", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			gen := behaviorBuilds.Add(1)
			if gen == 1 {
				return ReceiveFunc(func(_ Context, _ Envelope) error {
					failHit <- struct{}{}
					return errors.New("boom")
				}), nil
			}
			return ReceiveFunc(func(_ Context, env Envelope) error {
				if env.Payload == "ok" {
					restartedHandled <- struct{}{}
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "boom"); err != nil {
		t.Fatalf("Tell(boom) error = %v", err)
	}
	select {
	case <-failHit:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for failing message")
	}

	if err := sys.Tell(context.Background(), ref, "ok"); err != nil {
		t.Fatalf("Tell(ok) error = %v", err)
	}
	select {
	case <-restartedHandled:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for restarted behavior to handle message")
	}

	snap := obs.Snapshot()
	if snap.Restarts < 1 {
		t.Fatalf("restarts = %d, want >= 1", snap.Restarts)
	}
	if got := behaviorBuilds.Load(); got < 2 {
		t.Fatalf("behavior builds = %d, want >= 2", got)
	}
}

func TestMaxTotalQueuedLimit(t *testing.T) {
	sys, err := NewSystem(Config{
		MaxTotalQueued: 1,
		DefaultMailbox: NewBoundedMailboxFactory(BoundedMailboxConfig{Capacity: 16, FullPolicy: FailFast}),
	})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	ref, err := sys.Spawn(context.Background(), "queue-limit", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				select {
				case started <- struct{}{}:
				default:
				}
				<-gate
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "one"); err != nil {
		t.Fatalf("Tell(one) error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for first message start")
	}

	if err := sys.Tell(context.Background(), ref, "two"); err != nil {
		t.Fatalf("Tell(two) error = %v", err)
	}
	if err := sys.Tell(context.Background(), ref, "three"); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("Tell(three) error = %v, want %v", err, ErrMailboxFull)
	}
	close(gate)
}

func TestNoopHooksAreCallable(t *testing.T) {
	ctx := context.Background()

	var dead NopDeadLetterSink
	dead.HandleDeadLetter(ctx, DeadLetter{})

	var obs NopObserver
	obs.OnActorSpawn("a")
	obs.OnActorStop("a")
	obs.OnTellEnqueue("a")
	obs.OnAskStart("a")
	obs.OnAskDone("a", nil)
	obs.OnReceive("a", time.Millisecond, nil, nil)
	obs.OnRestart("a")
	obs.OnDeadLetter(DeadLetter{})

	var tracer NopTracer
	traceCtx, span := tracer.Start(ctx, "span", TraceAttrs{"k": "v"})
	if traceCtx == nil {
		t.Fatal("trace context is nil")
	}
	span.AddEvent("evt", nil)
	span.End(nil)
}
