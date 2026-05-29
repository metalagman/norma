package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/normahq/norma/actorlayer/dispatch"
)

type testRuntimeDelivery struct {
	env        any
	attempt    int
	max        int
	acked      bool
	retried    bool
	deadletter bool
	lastReason string
}

func (d *testRuntimeDelivery) Envelope() any { return d.env }
func (d *testRuntimeDelivery) Attempt() int {
	if d.attempt <= 0 {
		return 1
	}
	return d.attempt
}
func (d *testRuntimeDelivery) MaxAttempts() int               { return d.max }
func (*testRuntimeDelivery) InProgress(context.Context) error { return nil }
func (d *testRuntimeDelivery) Ack(context.Context) error {
	d.acked = true
	return nil
}
func (d *testRuntimeDelivery) Retry(context.Context, time.Duration, string) error {
	d.retried = true
	return nil
}
func (d *testRuntimeDelivery) DeadLetter(_ context.Context, reason string) error {
	d.lastReason = reason
	d.deadletter = true
	return nil
}

type envelope struct {
	to string
}

type testDispatchActor struct {
	address string
	err     error
	calls   int
	run     func(context.Context, any) error
}

func (a *testDispatchActor) Address() string { return a.address }
func (a *testDispatchActor) Handle(_ context.Context, _ any) error {
	a.calls++
	if a.run != nil {
		return a.run(context.Background(), nil)
	}
	return a.err
}

type testDispatchSource struct {
	items []Delivery
}

func (s testDispatchSource) Run(_ context.Context, handler Handler) error {
	for _, d := range s.items {
		if err := handler(context.Background(), d); err != nil {
			return err
		}
	}
	return nil
}

func retryNever() RetryPolicy {
	return RetryPolicy{IsRetryable: func(error) bool { return false }}
}

func retryAlways() RetryPolicy {
	return RetryPolicy{
		IsRetryable: func(error) bool { return true },
		Backoff:     func(int) time.Duration { return time.Millisecond },
	}
}

type testDelivery struct {
	env         any
	attempt     int
	maxAttempts int
	ackCalls    int32
	retryCalls  int32
	dlqCalls    int32
	lastDelay   time.Duration
	lastReason  string
}

func (d *testDelivery) Envelope() any { return d.env }
func (d *testDelivery) Attempt() int {
	if d.attempt <= 0 {
		return 1
	}
	return d.attempt
}
func (d *testDelivery) MaxAttempts() int { return d.maxAttempts }
func (*testDelivery) InProgress(context.Context) error {
	return nil
}
func (d *testDelivery) Ack(context.Context) error {
	atomic.AddInt32(&d.ackCalls, 1)
	return nil
}
func (d *testDelivery) Retry(_ context.Context, delay time.Duration, reason string) error {
	d.lastDelay = delay
	d.lastReason = reason
	atomic.AddInt32(&d.retryCalls, 1)
	return nil
}
func (d *testDelivery) DeadLetter(_ context.Context, reason string) error {
	d.lastReason = reason
	atomic.AddInt32(&d.dlqCalls, 1)
	return nil
}

type testResolver struct{ laneKey string }

func (r testResolver) LaneKey(Delivery) string { return r.laneKey }

type testSink struct {
	mu     sync.Mutex
	events []EventType
}

func (s *testSink) Publish(_ context.Context, event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event.Type)
}

func TestRuntimeHandleSuccessAcks(t *testing.T) {
	sink := &testSink{}
	rt, err := New(Config{Resolver: testResolver{laneKey: "k1"}, Sink: sink})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{}
	err = rt.Handle(context.Background(), d, func(context.Context, Delivery) error { return nil })
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if atomic.LoadInt32(&d.ackCalls) != 1 || atomic.LoadInt32(&d.retryCalls) != 0 || atomic.LoadInt32(&d.dlqCalls) != 0 {
		t.Fatalf("settlement counts = ack:%d retry:%d dlq:%d", d.ackCalls, d.retryCalls, d.dlqCalls)
	}
}

func TestRuntimeHandleRetryableRetries(t *testing.T) {
	rt, err := New(Config{
		Resolver: testResolver{laneKey: "k1"},
		Retry: RetryPolicy{
			IsRetryable:    func(error) bool { return true },
			Backoff:        func(int) time.Duration { return 25 * time.Millisecond },
			RetryExhausted: func(Delivery) bool { return false },
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{}
	err = rt.Handle(context.Background(), d, func(context.Context, Delivery) error { return errors.New("retry") })
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if atomic.LoadInt32(&d.retryCalls) != 1 || d.lastDelay != 25*time.Millisecond {
		t.Fatalf("retry calls=%d delay=%v", d.retryCalls, d.lastDelay)
	}
}

func TestRuntimeHandlePermanentDeadLetters(t *testing.T) {
	rt, err := New(Config{
		Resolver: testResolver{laneKey: "k1"},
		Retry:    RetryPolicy{IsRetryable: func(error) bool { return false }},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{}
	err = rt.Handle(context.Background(), d, func(context.Context, Delivery) error { return errors.New("permanent") })
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if atomic.LoadInt32(&d.dlqCalls) != 1 {
		t.Fatalf("deadletter calls=%d", d.dlqCalls)
	}
}

func TestRuntimeHandleRetryExhaustionDeadLetters(t *testing.T) {
	rt, err := New(Config{
		Resolver: testResolver{laneKey: "k1"},
		Retry: RetryPolicy{
			IsRetryable:    func(error) bool { return true },
			RetryExhausted: func(Delivery) bool { return true },
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	d := &testDelivery{}
	err = rt.Handle(context.Background(), d, func(context.Context, Delivery) error { return errors.New("boom") })
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if atomic.LoadInt32(&d.dlqCalls) != 1 {
		t.Fatalf("deadletter calls=%d", d.dlqCalls)
	}
}

func TestRuntimeLaneSerialization(t *testing.T) {
	rt, err := New(Config{Resolver: testResolver{laneKey: "same"}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	run := func(id string) {
		d := &testDelivery{env: id}
		done <- rt.Handle(context.Background(), d, func(context.Context, Delivery) error {
			started <- id
			if id == "first" {
				<-release
			}
			return nil
		})
	}
	go run("first")
	if got := waitStart(t, started); got != "first" {
		t.Fatalf("first started=%q", got)
	}
	go run("second")
	select {
	case got := <-started:
		t.Fatalf("second started too early: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := waitStart(t, started); got != "second" {
		t.Fatalf("second started=%q", got)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
	}
}

func TestRuntimeDifferentLanesConcurrent(t *testing.T) {
	rt, err := New(Config{Resolver: resolverFunc(func(d Delivery) string { return d.Envelope().(string) })})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	release := make(chan struct{})
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	run := func(id string, done chan<- error) {
		d := &testDelivery{env: id}
		done <- rt.Handle(context.Background(), d, func(context.Context, Delivery) error {
			current := inFlight.Add(1)
			for {
				seen := maxInFlight.Load()
				if current <= seen || maxInFlight.CompareAndSwap(seen, current) {
					break
				}
			}
			<-release
			inFlight.Add(-1)
			return nil
		})
	}
	done := make(chan error, 2)
	go run("lane-1", done)
	go run("lane-2", done)
	deadline := time.After(time.Second)
	for maxInFlight.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("never overlapped, max=%d", maxInFlight.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
	}
}

func TestRuntimeLaneStatusReportsActiveLanes(t *testing.T) {
	rt, err := New(Config{Resolver: testResolver{laneKey: "lane-1"}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	started := make(chan struct{})
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	completed := make(chan struct{}, 2)
	go func() {
		_ = rt.Handle(context.Background(), &testDelivery{env: "task-a"}, func(context.Context, Delivery) error {
			close(started)
			<-releaseA
			completed <- struct{}{}
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first handler start")
	}

	status := rt.LaneStatus()
	if status.Active != 1 {
		t.Fatalf("LaneStatus().Active = %d, want 1", status.Active)
	}

	go func() {
		_ = rt.Handle(context.Background(), &testDelivery{env: "task-b"}, func(context.Context, Delivery) error {
			<-releaseB
			completed <- struct{}{}
			return nil
		})
	}()

	close(releaseA)
	<-completed
	close(releaseB)
	<-completed

	deadline := time.After(time.Second)
	for {
		status := rt.LaneStatus()
		if status.Active == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for lanes to clear: %#v", status)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestNewDispatchRuntimeReturnsErrorForMissingRegistry(t *testing.T) {
	rt, err := NewDispatchRuntime(RuntimeConfig{AddressOf: func(any) (string, error) { return "", nil }, Retry: retryNever()})
	if rt != nil {
		t.Fatalf("NewDispatchRuntime() runtime = %#v, want nil", rt)
	}
	if err == nil || err.Error() != "runtime registry is required" {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
}

func TestNewDispatchRuntimeReturnsErrorForMissingAddressResolver(t *testing.T) {
	rt, err := NewDispatchRuntime(RuntimeConfig{Registry: dispatch.NewMemoryRegistry()})
	if rt != nil {
		t.Fatalf("NewDispatchRuntime() runtime = %#v, want nil", rt)
	}
	if err == nil || err.Error() != "runtime address resolver is required" {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
}

func TestDispatchRuntimeHandleAckOnSuccess(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	actor := &testDispatchActor{address: "session:*"}
	if err := registry.Register(actor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			v, ok := env.(envelope)
			if !ok {
				return "", fmt.Errorf("bad envelope")
			}
			return v.to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	d := &testRuntimeDelivery{env: envelope{to: "session:s-1"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.acked {
		t.Fatal("Ack() was not called")
	}
	if actor.calls != 1 {
		t.Fatalf("actor calls = %d, want 1", actor.calls)
	}
}

func TestDispatchRuntimeDeadlettersOnResolveFailure(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			v := env.(envelope)
			return v.to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	d := &testRuntimeDelivery{env: envelope{to: "session:s-404"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.deadletter {
		t.Fatal("DeadLetter() was not called")
	}
}

func TestDispatchRuntimeLaneStatus(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	release := make(chan struct{})
	actor := &testDispatchActor{
		address: "session:*",
		run: func(_ context.Context, _ any) error {
			<-release
			return nil
		},
	}
	if err := registry.Register(actor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			return env.(envelope).to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	if got := rt.LaneStatus().Active; got != 0 {
		t.Fatalf("LaneStatus().Active = %d, want 0", got)
	}
	done := make(chan error, 1)
	go func() {
		done <- rt.Handle(context.Background(), &testRuntimeDelivery{env: envelope{to: "session:s-1"}})
	}()
	deadline := time.After(time.Second)
	for {
		status := rt.LaneStatus()
		if status.Active == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for in-flight lane, status = %#v", status)
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := rt.LaneStatus().Active; got != 0 {
		t.Fatalf("LaneStatus().Active after completion = %d, want 0", got)
	}
}

func TestDispatchRuntimeRetriesOnActorError(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	if err := registry.Register(&testDispatchActor{address: "session:*", err: errors.New("temporary")}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			v := env.(envelope)
			return v.to, nil
		},
		Retry: retryAlways(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	d := &testRuntimeDelivery{env: envelope{to: "session:s-1"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !d.retried {
		t.Fatal("Retry() was not called")
	}
}

func TestDispatchRuntimeRunsSource(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	actor := &testDispatchActor{address: "session:*"}
	if err := registry.Register(actor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			v := env.(envelope)
			return v.to, nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	deliveries := []Delivery{
		&testRuntimeDelivery{env: envelope{to: "session:s-1"}},
		&testRuntimeDelivery{env: envelope{to: "session:s-2"}},
	}
	source := testDispatchSource{items: deliveries}
	if err := rt.Run(context.Background(), source); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := actor.calls; got != 2 {
		t.Fatalf("actor calls = %d, want 2", got)
	}
}

func TestDispatchRuntimeResolvesCaseInsensitiveAddress(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	actor := &testDispatchActor{address: "session:*"}
	if err := registry.Register(actor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry: registry,
		AddressOf: func(env any) (string, error) {
			return "  sEsSiOn:*  ", nil
		},
		Retry: retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	d := &testRuntimeDelivery{env: envelope{to: "x"}}
	if err := rt.Handle(context.Background(), d); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if actor.calls != 1 {
		t.Fatalf("actor calls = %d, want 1", actor.calls)
	}
}

func TestDispatchRuntimeRejectsBlankAddress(t *testing.T) {
	registry := dispatch.NewMemoryRegistry()
	if err := registry.Register(&testDispatchActor{address: "session:*"}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	rt, err := NewDispatchRuntime(RuntimeConfig{
		Registry:  registry,
		AddressOf: func(env any) (string, error) { return "   ", nil },
		Retry:     retryNever(),
	})
	if err != nil {
		t.Fatalf("NewDispatchRuntime() error = %v", err)
	}
	delivery := &testRuntimeDelivery{env: envelope{to: "x"}}
	err = rt.Handle(context.Background(), delivery)
	if err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if !delivery.deadletter {
		t.Fatal("DeadLetter() was not called")
	}
	if delivery.lastReason != "empty actor address" {
		t.Fatalf("DeadLetter reason = %q, want %q", delivery.lastReason, "empty actor address")
	}
}

type resolverFunc func(Delivery) string

func (f resolverFunc) LaneKey(d Delivery) string { return f(d) }

func waitStart(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start")
		return ""
	}
}
