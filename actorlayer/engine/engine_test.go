package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
