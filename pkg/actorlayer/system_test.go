package actorlayer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

type traceRecord struct {
	name string
	err  error
}

type recordingTracer struct {
	mu    sync.Mutex
	spans []traceRecord
}

func (t *recordingTracer) Start(ctx context.Context, name string, _ TraceAttrs) (context.Context, Span) {
	return ctx, traceSpan{name: name, tracer: t}
}

type traceSpan struct {
	name   string
	tracer *recordingTracer
}

func (s traceSpan) AddEvent(string, TraceAttrs) {}

func (s traceSpan) End(err error) {
	s.tracer.mu.Lock()
	s.tracer.spans = append(s.tracer.spans, traceRecord{name: s.name, err: err})
	s.tracer.mu.Unlock()
}

func (t *recordingTracer) names() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.spans))
	for _, span := range t.spans {
		out = append(out, span.name)
	}
	return out
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestSequentialProcessingPerActor(t *testing.T) {
	t.Parallel()

	sys, err := NewSystem(Config{
		DefaultMailbox: NewBoundedMailboxFactory(BoundedMailboxConfig{Capacity: 256, FullPolicy: FailFast}),
	})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	var inFlight atomic.Int32
	var overlap atomic.Bool
	var processed atomic.Int32
	const total = 100
	done := make(chan struct{})

	ref, err := sys.Spawn(context.Background(), "seq", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				if inFlight.Add(1) != 1 {
					overlap.Store(true)
				}
				time.Sleep(1 * time.Millisecond)
				inFlight.Add(-1)
				if processed.Add(1) == total {
					close(done)
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	for i := 0; i < total; i++ {
		if err := sys.Tell(context.Background(), ref, i); err != nil {
			t.Fatalf("Tell() error = %v", err)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for messages to process")
	}

	if overlap.Load() {
		t.Fatal("actor processed messages concurrently; expected strict serialization")
	}
}

func TestDifferentActorsProcessConcurrently(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	newBlocking := func(name string) Ref {
		t.Helper()
		ref, err := sys.Spawn(context.Background(), name, Props{
			NewBehavior: func(SpawnContext) (Behavior, error) {
				return ReceiveFunc(func(_ Context, _ Envelope) error {
					started <- struct{}{}
					<-gate
					wg.Done()
					return nil
				}), nil
			},
		})
		if err != nil {
			t.Fatalf("Spawn(%s) error = %v", name, err)
		}
		return ref
	}

	a := newBlocking("a")
	b := newBlocking("b")

	start := time.Now()
	if err := sys.Tell(context.Background(), a, "work"); err != nil {
		t.Fatalf("Tell(a) error = %v", err)
	}
	if err := sys.Tell(context.Background(), b, "work"); err != nil {
		t.Fatalf("Tell(b) error = %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("expected both actors to start processing")
		}
	}

	close(gate)
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		wg.Wait()
	}()

	select {
	case <-waitDone:
	case <-time.After(1 * time.Second):
		t.Fatal("actors did not finish")
	}

	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Fatalf("actors appear serialized across cells; elapsed=%v", elapsed)
	}
}

func TestTellFailsWhenMailboxFullFailFast(t *testing.T) {
	t.Parallel()

	sys, err := NewSystem(Config{
		DefaultMailbox: NewBoundedMailboxFactory(BoundedMailboxConfig{Capacity: 1, FullPolicy: FailFast}),
	})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	ref, err := sys.Spawn(context.Background(), "full", Props{
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

	if err := sys.Tell(context.Background(), ref, 1); err != nil {
		t.Fatalf("Tell(1) error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first message was not picked up by actor")
	}
	// First message blocks in receive; second should occupy the single queue slot.
	if err := sys.Tell(context.Background(), ref, 2); err != nil {
		t.Fatalf("Tell(2) error = %v", err)
	}
	if err := sys.Tell(context.Background(), ref, 3); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("Tell(3) error = %v, want %v", err, ErrMailboxFull)
	}

	close(gate)
}

func TestAskTimeout(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	ref, err := sys.Spawn(context.Background(), "slow", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				time.Sleep(250 * time.Millisecond)
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	_, err = Ask(context.Background(), sys, ref, "ping", WithTimeout(50*time.Millisecond))
	if !errors.Is(err, ErrAskTimeout) {
		t.Fatalf("Ask() error = %v, want %v", err, ErrAskTimeout)
	}
}

func TestStopSendsTerminatedToWatchers(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	terminated := make(chan Terminated, 1)
	watcher, err := sys.Spawn(context.Background(), "watcher", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				msg, ok := env.Payload.(Terminated)
				if ok {
					terminated <- msg
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(watcher) error = %v", err)
	}

	target, err := sys.Spawn(context.Background(), "target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	if err := sys.Watch(context.Background(), watcher, target); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if err := sys.Stop(context.Background(), target); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case msg := <-terminated:
		if msg.ActorID != target.ID() {
			t.Fatalf("Terminated.ActorID = %q, want %q", msg.ActorID, target.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive terminated message")
	}
}

func TestPanicTriggersSupervisorDirective(t *testing.T) {
	t.Parallel()

	sup := &recordingSupervisor{directive: Stop, decided: make(chan Failure, 1)}
	sys, err := NewSystem(Config{Supervision: sup})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	ref, err := sys.Spawn(context.Background(), "panic", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				panic("boom")
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "go"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case failure := <-sup.decided:
		if failure.Panic == nil {
			t.Fatal("expected panic value in failure")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("supervisor did not receive failure")
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		err := sys.Tell(context.Background(), ref, "again")
		if errors.Is(err, ErrActorNotFound) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actor should have been stopped by supervisor directive")
}

func TestTellRateLimitPerSecond(t *testing.T) {
	t.Parallel()

	clock := &testClock{now: time.Unix(1_700_000_000, 0)}
	sys, err := NewSystem(Config{
		Clock:                  clock,
		TellRateLimitPerSecond: 1,
	})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	ref, err := sys.Spawn(context.Background(), "rl", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "one"); err != nil {
		t.Fatalf("Tell(one) error = %v", err)
	}
	if err := sys.Tell(context.Background(), ref, "two"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Tell(two) error = %v, want %v", err, ErrRateLimited)
	}

	clock.Advance(1 * time.Second)
	if err := sys.Tell(context.Background(), ref, "three"); err != nil {
		t.Fatalf("Tell(three) after window reset error = %v", err)
	}
}

func TestShutdownDrainWaitsForQueuedMessages(t *testing.T) {
	t.Parallel()

	sys, err := NewSystem(Config{
		ShutdownPolicy:       ShutdownDrain,
		ShutdownPollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}

	var processed atomic.Int32
	ref, err := sys.Spawn(context.Background(), "drain", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				time.Sleep(15 * time.Millisecond)
				processed.Add(1)
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	const total = 5
	for i := 0; i < total; i++ {
		if err := sys.Tell(context.Background(), ref, i); err != nil {
			t.Fatalf("Tell(%d) error = %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := sys.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if got := processed.Load(); got != total {
		t.Fatalf("processed = %d, want %d", got, total)
	}
}

func TestObserverCollectsCounters(t *testing.T) {
	t.Parallel()

	obs := &StatsObserver{}
	sys, err := NewSystem(Config{Observer: obs})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	ref, err := sys.Spawn(context.Background(), "obs", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "ok"); err != nil {
		t.Fatalf("Tell(ok) error = %v", err)
	}

	badRef := Ref{id: ActorID("missing"), sys: sys}
	if err := sys.Tell(context.Background(), badRef, "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("Tell(missing) error = %v, want %v", err, ErrActorNotFound)
	}

	time.Sleep(20 * time.Millisecond)
	snap := obs.Snapshot()
	if snap.ActorSpawns < 1 {
		t.Fatalf("ActorSpawns = %d, want >=1", snap.ActorSpawns)
	}
	if snap.TellEnqueues < 1 {
		t.Fatalf("TellEnqueues = %d, want >=1", snap.TellEnqueues)
	}
	if snap.DeadLetters < 1 {
		t.Fatalf("DeadLetters = %d, want >=1", snap.DeadLetters)
	}
}

func TestTracingHooksEmitCoreSpans(t *testing.T) {
	t.Parallel()

	tracer := &recordingTracer{}
	sys, err := NewSystem(Config{Tracer: tracer})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	defer shutdownSystem(t, sys)

	actorRef, err := sys.Spawn(context.Background(), "trace", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, env Envelope) error {
				if env.ReplyTo != nil {
					return ctx.Tell(ctx, *env.ReplyTo, "ok")
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), actorRef, "fire"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}
	if _, err := Ask(context.Background(), sys, actorRef, "req", WithTimeout(300*time.Millisecond)); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	time.Sleep(25 * time.Millisecond)
	names := tracer.names()
	requireSpan := func(name string) {
		t.Helper()
		for _, got := range names {
			if got == name {
				return
			}
		}
		t.Fatalf("span %q not found in %v", name, names)
	}

	requireSpan("actor.spawn")
	requireSpan("actor.tell")
	requireSpan("actor.ask")
	requireSpan("actor.receive")
}

func TestContextPublishDeliversToSubscribers(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	received := make(chan PublishedMessage, 1)
	subscriber, err := sys.Spawn(context.Background(), "sub", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				msg, ok := env.Payload.(PublishedMessage)
				if ok {
					received <- msg
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(subscriber) error = %v", err)
	}

	if err := sys.Subscribe("events.topic", subscriber); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	publisher, err := sys.Spawn(context.Background(), "pub", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				return ctx.Publish(ctx, "events.topic", map[string]any{"k": "v"})
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(publisher) error = %v", err)
	}

	if err := sys.Tell(context.Background(), publisher, "publish"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case got := <-received:
		if got.Topic != "events.topic" {
			t.Fatalf("topic = %q, want events.topic", got.Topic)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive published message")
	}
}

func TestLifecycleEventsPublishedOnSpawnAndStop(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	started := make(chan Started, 4)
	stopped := make(chan Stopped, 4)

	subscriber, err := sys.Spawn(context.Background(), "lifecycle-sub", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				msg, ok := env.Payload.(PublishedMessage)
				if !ok {
					return nil
				}
				switch payload := msg.Payload.(type) {
				case Started:
					started <- payload
				case Stopped:
					stopped <- payload
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(subscriber) error = %v", err)
	}

	if err := sys.Subscribe(LifecycleTopic, subscriber); err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	target, err := sys.Spawn(context.Background(), "lifecycle-target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	waitForStarted := func(want ActorID) {
		t.Helper()
		timeout := time.After(500 * time.Millisecond)
		for {
			select {
			case got := <-started:
				if got.Actor.ID() == want {
					return
				}
			case <-timeout:
				t.Fatalf("did not receive Started event for %q", want)
			}
		}
	}

	waitForStopped := func(want ActorID) {
		t.Helper()
		timeout := time.After(500 * time.Millisecond)
		for {
			select {
			case got := <-stopped:
				if got.Actor.ID() == want {
					return
				}
			case <-timeout:
				t.Fatalf("did not receive Stopped event for %q", want)
			}
		}
	}

	waitForStarted(target.ID())
	if err := sys.Stop(context.Background(), target); err != nil {
		t.Fatalf("Stop(target) error = %v", err)
	}
	waitForStopped(target.ID())
}

func TestStopDeliversTerminatedWhileSystemShuttingDown(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	terminated := make(chan Terminated, 1)
	watcher, err := sys.Spawn(context.Background(), "watcher-shutdown", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				if msg, ok := env.Payload.(Terminated); ok {
					terminated <- msg
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(watcher) error = %v", err)
	}

	target, err := sys.Spawn(context.Background(), "target-shutdown", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	if err := sys.Watch(context.Background(), watcher, target); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	sys.shuttingDown.Store(true)
	if err := sys.Stop(context.Background(), target); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case msg := <-terminated:
		if msg.ActorID != target.ID() {
			t.Fatalf("Terminated.ActorID = %q, want %q", msg.ActorID, target.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive terminated message while shutting down")
	}
}

func TestContextTellIgnoresSenderOverride(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	gotSender := make(chan ActorID, 1)
	target, err := sys.Spawn(context.Background(), "tell-target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				if s := ctx.Sender(); s != nil {
					gotSender <- s.ID()
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	spoof := sys.Ref("spoof")
	sender, err := sys.Spawn(context.Background(), "tell-sender", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				return ctx.Tell(ctx, target, "payload", WithFrom(spoof))
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(sender) error = %v", err)
	}

	if err := sys.Tell(context.Background(), sender, "go"); err != nil {
		t.Fatalf("Tell(sender) error = %v", err)
	}

	select {
	case got := <-gotSender:
		if got != sender.ID() {
			t.Fatalf("sender id = %q, want %q", got, sender.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive sender id")
	}
}

func TestContextAskIgnoresSenderOverride(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	resolved := make(chan ActorID, 1)
	target, err := sys.Spawn(context.Background(), "ask-target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, env Envelope) error {
				if env.ReplyTo == nil {
					return nil
				}
				var senderID ActorID
				if s := ctx.Sender(); s != nil {
					senderID = s.ID()
				}
				return ctx.Tell(ctx, *env.ReplyTo, senderID)
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	spoof := sys.Ref("spoof")
	requester, err := sys.Spawn(context.Background(), "ask-requester", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				reply, askErr := ctx.Ask(ctx, target, "req", WithAskFrom(spoof))
				if askErr != nil {
					return askErr
				}
				if id, ok := reply.Payload.(ActorID); ok {
					resolved <- id
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(requester) error = %v", err)
	}

	if err := sys.Tell(context.Background(), requester, "go"); err != nil {
		t.Fatalf("Tell(requester) error = %v", err)
	}

	select {
	case got := <-resolved:
		if got != requester.ID() {
			t.Fatalf("ask sender id = %q, want %q", got, requester.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive ask sender id")
	}
}

func TestContextTellAfterIgnoresSenderOverride(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	gotSender := make(chan ActorID, 1)
	target, err := sys.Spawn(context.Background(), "after-target", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				if s := ctx.Sender(); s != nil {
					gotSender <- s.ID()
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(target) error = %v", err)
	}

	spoof := sys.Ref("spoof")
	scheduler, err := sys.Spawn(context.Background(), "after-scheduler", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(ctx Context, _ Envelope) error {
				_, callErr := ctx.TellAfter(ctx, 10*time.Millisecond, target, "payload", WithFrom(spoof))
				return callErr
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(scheduler) error = %v", err)
	}

	if err := sys.Tell(context.Background(), scheduler, "go"); err != nil {
		t.Fatalf("Tell(scheduler) error = %v", err)
	}

	select {
	case got := <-gotSender:
		if got != scheduler.ID() {
			t.Fatalf("tellAfter sender id = %q, want %q", got, scheduler.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive tellAfter sender id")
	}
}

func TestStopPrunesSubscriberFromTopics(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	sub, err := sys.Spawn(context.Background(), "sub-prune", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(sub) error = %v", err)
	}

	if err := sys.Subscribe("topic.a", sub); err != nil {
		t.Fatalf("Subscribe(topic.a) error = %v", err)
	}
	if err := sys.Subscribe("topic.b", sub); err != nil {
		t.Fatalf("Subscribe(topic.b) error = %v", err)
	}
	if err := sys.Stop(context.Background(), sub); err != nil {
		t.Fatalf("Stop(sub) error = %v", err)
	}

	sys.subMu.RLock()
	defer sys.subMu.RUnlock()
	for topic, set := range sys.subscribers {
		if _, ok := set[sub.ID()]; ok {
			t.Fatalf("subscriber %q still present in topic %q", sub.ID(), topic)
		}
	}
}

func TestSpawnConcurrentDuplicateActorIDOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	start := make(chan struct{})
	errs := make(chan error, 2)
	const actorID = "same-id"

	spawnOnce := func() {
		<-start
		_, err := sys.Spawn(context.Background(), "dup", Props{
			NewBehavior: func(SpawnContext) (Behavior, error) {
				return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
			},
		}, WithActorID(actorID))
		errs <- err
	}

	go spawnOnce()
	go spawnOnce()
	close(start)

	var success int
	var failed int
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			success++
		} else {
			failed++
		}
	}

	if success != 1 || failed != 1 {
		t.Fatalf("spawn results success=%d failed=%d, want success=1 failed=1", success, failed)
	}
}

func TestStopHonorsContextTimeoutWhileActorIsBusy(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	started := make(chan struct{}, 1)
	release := make(chan struct{})

	ref, err := sys.Spawn(context.Background(), "busy-stop", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "run"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("actor did not start processing")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	stopErr := sys.Stop(stopCtx, ref)
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want %v", stopErr, context.DeadlineExceeded)
	}

	close(release)
}

func TestSpawnWithSameIDFailsWhilePreviousActorStillStopping(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	const actorID = "reused-id"

	ref, err := sys.Spawn(context.Background(), "busy-stop", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release
				return nil
			}), nil
		},
	}, WithActorID(actorID))
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	if err := sys.Tell(context.Background(), ref, "run"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("actor did not start processing")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	stopErr := sys.Stop(stopCtx, ref)
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want %v", stopErr, context.DeadlineExceeded)
	}

	if _, err := sys.Spawn(context.Background(), "busy-stop", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	}, WithActorID(actorID)); err == nil {
		t.Fatal("Spawn() error = nil, want actor already exists while prior actor is stopping")
	}

	close(release)
	deadline := time.Now().Add(1 * time.Second)
	for sys.getActor(actorID) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sys.getActor(actorID) != nil {
		t.Fatal("actor was not removed after stop completed")
	}

	reused, err := sys.Spawn(context.Background(), "busy-stop", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	}, WithActorID(actorID))
	if err != nil {
		t.Fatalf("Spawn() after stop completion error = %v", err)
	}
	if err := sys.Stop(context.Background(), reused); err != nil {
		t.Fatalf("Stop(reused) error = %v", err)
	}
}

func TestSpawnReservationClearedWhenBehaviorInitPanics(t *testing.T) {
	t.Parallel()

	sys := newTestSystem(t)
	defer shutdownSystem(t, sys)

	const actorID = "panic-init-id"

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Spawn() did not panic")
			}
		}()
		_, _ = sys.Spawn(context.Background(), "panic-init", Props{
			NewBehavior: func(SpawnContext) (Behavior, error) {
				panic("init panic")
			},
		}, WithActorID(actorID))
	}()

	ref, err := sys.Spawn(context.Background(), "panic-init", Props{
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, _ Envelope) error { return nil }), nil
		},
	}, WithActorID(actorID))
	if err != nil {
		t.Fatalf("Spawn() after panic error = %v", err)
	}
	if err := sys.Stop(context.Background(), ref); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

type recordingSupervisor struct {
	directive Directive
	decided   chan Failure
}

func (r *recordingSupervisor) Decide(_ SupervisionContext, failure Failure) Directive {
	select {
	case r.decided <- failure:
	default:
	}
	return r.directive
}

func newTestSystem(t *testing.T) *System {
	t.Helper()
	sys, err := NewSystem(Config{})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	return sys
}

func shutdownSystem(t *testing.T, sys *System) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sys.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
