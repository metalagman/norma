package actorlayer

import (
	"sync/atomic"
	"time"
)

// Observer receives runtime lifecycle and messaging callbacks from System.
type Observer interface {
	OnActorSpawn(actorID ActorID)
	OnActorStop(actorID ActorID)
	OnTellEnqueue(actorID ActorID)
	OnAskStart(actorID ActorID)
	OnAskDone(actorID ActorID, err error)
	OnReceive(actorID ActorID, duration time.Duration, err error, panicValue any)
	OnRestart(actorID ActorID)
	OnDeadLetter(letter DeadLetter)
}

// NopObserver is a no-op Observer implementation.
type NopObserver struct{}

// OnActorSpawn records actor spawn events.
func (NopObserver) OnActorSpawn(ActorID) {}

// OnActorStop records actor stop events.
func (NopObserver) OnActorStop(ActorID) {}

// OnTellEnqueue records Tell enqueue events.
func (NopObserver) OnTellEnqueue(ActorID) {}

// OnAskStart records Ask start events.
func (NopObserver) OnAskStart(ActorID) {}

// OnAskDone records Ask completion events.
func (NopObserver) OnAskDone(ActorID, error) {}

// OnReceive records message receive events.
func (NopObserver) OnReceive(ActorID, time.Duration, error, any) {}

// OnRestart records actor restart events.
func (NopObserver) OnRestart(ActorID) {}

// OnDeadLetter records dead-letter events.
func (NopObserver) OnDeadLetter(DeadLetter) {}

// StatsObserver accumulates runtime counters and durations.
type StatsObserver struct {
	actorSpawns     atomic.Int64
	actorStops      atomic.Int64
	tellEnqueues    atomic.Int64
	askStarts       atomic.Int64
	askFailures     atomic.Int64
	receives        atomic.Int64
	receiveFailures atomic.Int64
	receivePanics   atomic.Int64
	restarts        atomic.Int64
	deadletters     atomic.Int64
	receiveDurNSum  atomic.Int64
}

// StatsSnapshot contains cumulative counters collected by StatsObserver.
type StatsSnapshot struct {
	ActorSpawns     int64
	ActorStops      int64
	TellEnqueues    int64
	AskStarts       int64
	AskFailures     int64
	Receives        int64
	ReceiveFailures int64
	ReceivePanics   int64
	Restarts        int64
	DeadLetters     int64
	ReceiveDurTotal time.Duration
}

// OnActorSpawn increments the actor spawn counter.
func (s *StatsObserver) OnActorSpawn(ActorID) {
	s.actorSpawns.Add(1)
}

// OnActorStop increments the actor stop counter.
func (s *StatsObserver) OnActorStop(ActorID) {
	s.actorStops.Add(1)
}

// OnTellEnqueue increments the enqueue counter.
func (s *StatsObserver) OnTellEnqueue(ActorID) {
	s.tellEnqueues.Add(1)
}

// OnAskStart increments the ask-start counter.
func (s *StatsObserver) OnAskStart(ActorID) {
	s.askStarts.Add(1)
}

// OnAskDone increments the ask-failure counter for failed asks.
func (s *StatsObserver) OnAskDone(_ ActorID, err error) {
	if err != nil {
		s.askFailures.Add(1)
	}
}

// OnReceive updates counters and total receive duration.
func (s *StatsObserver) OnReceive(_ ActorID, duration time.Duration, err error, panicValue any) {
	s.receives.Add(1)
	s.receiveDurNSum.Add(duration.Nanoseconds())
	if err != nil {
		s.receiveFailures.Add(1)
	}
	if panicValue != nil {
		s.receivePanics.Add(1)
	}
}

// OnRestart increments the restart counter.
func (s *StatsObserver) OnRestart(ActorID) {
	s.restarts.Add(1)
}

// OnDeadLetter increments the dead-letter counter.
func (s *StatsObserver) OnDeadLetter(DeadLetter) {
	s.deadletters.Add(1)
}

// Snapshot returns a point-in-time copy of collected stats.
func (s *StatsObserver) Snapshot() StatsSnapshot {
	return StatsSnapshot{
		ActorSpawns:     s.actorSpawns.Load(),
		ActorStops:      s.actorStops.Load(),
		TellEnqueues:    s.tellEnqueues.Load(),
		AskStarts:       s.askStarts.Load(),
		AskFailures:     s.askFailures.Load(),
		Receives:        s.receives.Load(),
		ReceiveFailures: s.receiveFailures.Load(),
		ReceivePanics:   s.receivePanics.Load(),
		Restarts:        s.restarts.Load(),
		DeadLetters:     s.deadletters.Load(),
		ReceiveDurTotal: time.Duration(s.receiveDurNSum.Load()),
	}
}
