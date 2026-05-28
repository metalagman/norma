# Actorlayer: Actor Runtime over ADK

`actorlayer` is an in-process actor runtime for `norma`. It provides actor-model runtime semantics (addressable refs, mailboxes, lifecycle, supervision) and hosts ADK agents through `actorlayer/adkactor`.

This document tracks the implementation based on ADR-0001 (`actorlayer` over ADK).

## Package Layout

- `actorlayer/`: core actor runtime.
- `actorlayer/adkactor/`: ADK adapter layer.
- `actorlayer/persistence/`: persistence extension interfaces.

## Current Capabilities

### Core runtime

- Stable actor IDs and `Ref`.
- `Tell` (async) and `Ask` (request/reply with timeout).
- Per-actor FIFO mailbox with one-message-at-a-time execution.
- Mailbox backpressure policies:
  - `FailFast`
  - `BlockUntilSpace`
  - `DropNewest`
  - `DropOldest`
- Supervision directives:
  - `Resume`
  - `Restart`
  - `Stop`
  - `Escalate`
- Optional threshold supervision policy:
  - `NewThresholdSupervisor(...)` for restart-failure windows and escalation/stop on threshold exceed.
- Dead-letter sink.
- Watchers with `Terminated` notifications.
- Runtime lifecycle publish events on `actor.lifecycle` (`Started`, `Stopped`).

### Runtime hardening

- Global tell rate limiting (`TellRateLimitPerSecond`).
- Graceful drain shutdown (`ShutdownDrain`) with polling interval.
- Delayed send scheduling (`TellAfter`) with cancellation and completion result.
- Observer hooks (`Observer`) with `StatsObserver` counters.
- Tracing hooks (`Tracer`) with span points for spawn/tell/ask/receive.

### ADK adapter (`adkactor`)

- `Props(cfg)` for spawning ADK-hosted actors.
- Session/user mapping policies:
  - `ConversationSession`
  - `PerActorSession`
  - `PerMessageSession`
  - `HeaderUser`
  - `StaticUserPolicy`
- Codecs:
  - `TextCodec`
  - `JSONCodec`
  - `ContentCodec`
- Reply modes:
  - `NoReply`
  - `ReplyFinal`
  - `ReplyEachEvent`
  - `ReplyFinalAndPublishEvents` (see limitations)
- ADK tools:
  - `actor_send`
  - `actor_ask`
  - `actor_publish` (opt-in)
- Tool safety controls:
  - address policy
  - named refs
  - optional direct actor ID targeting
  - ask timeout bounds
  - payload size limit

## Quick Example

```go
sys, err := actorlayer.NewSystem(actorlayer.Config{})
if err != nil {
	return err
}

counter, err := sys.Spawn(ctx, "counter", actorlayer.Props{
	NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
		var n int
		return actorlayer.ReceiveFunc(func(c actorlayer.Context, env actorlayer.Envelope) error {
			switch env.Payload {
			case "inc":
				n++
				return nil
			case "get":
				if env.ReplyTo != nil {
					return c.Tell(c, *env.ReplyTo, n)
				}
			}
			return nil
		}), nil
	},
})
if err != nil {
	return err
}

_ = sys.Tell(ctx, counter, "inc")
reply, err := actorlayer.Ask(ctx, sys, counter, "get", actorlayer.WithTimeout(3*time.Second))
if err != nil {
	return err
}
fmt.Println(reply.Payload)
```

## Scheduler and Lifecycle Example

```go
sys, _ := actorlayer.NewSystem(actorlayer.Config{})

observer, _ := sys.Spawn(ctx, "observer", actorlayer.Props{
	NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
		return actorlayer.ReceiveFunc(func(_ actorlayer.Context, env actorlayer.Envelope) error {
			if pub, ok := env.Payload.(actorlayer.PublishedMessage); ok && pub.Topic == actorlayer.LifecycleTopic {
				// handle Started/Stopped events
			}
			return nil
		}), nil
	},
})

_ = sys.Subscribe(actorlayer.LifecycleTopic, observer)
scheduled, _ := sys.TellAfter(ctx, 250*time.Millisecond, observer, "tick")
_ = <-scheduled.Done()
```

## Threshold Supervision Example

```go
sup := actorlayer.NewThresholdSupervisor(actorlayer.ThresholdSupervisorConfig{
	Base:        actorlayer.DefaultSupervisor{},
	MaxRestarts: 3,
	Window:      30 * time.Second,
	OnExceeded:  actorlayer.Escalate,
})

sys, _ := actorlayer.NewSystem(actorlayer.Config{
	Supervision: sup,
})
```

## Verification Coverage

`actorlayer` and `adkactor` tests cover:

- sequential processing for one actor
- concurrency across actors
- mailbox full behavior under `FailFast`
- ask timeout
- watcher termination notification
- supervision panic path
- ADK runner invocation and reply mapping
- session policy mapping
- non-overlap for same ADK actor
- toolset allow/deny, payload limits, timeout limits
- drain shutdown behavior
- tell rate limiting
- observer and tracer span hooks

## Known Limitations and Remaining ADR Work

- Persistence package currently defines interfaces only (no concrete durable stores).
- Cluster/remote transport is not implemented.
- Lifecycle model is still local-process only (no remote lifecycle propagation yet).
- No concrete OpenTelemetry/Prometheus adapters yet (hooks are present).

## Recommended Next Steps

1. Add concrete persistence adapters for mailbox/state.
2. Add concrete tracing/metrics adapters and wire to deployment defaults.
3. Add cluster/remote transport and placement components.
