# Actorlayer: Provider-Agnostic Typed Actor Engine

`actorlayer` is Norma's provider-agnostic typed actor engine. It provides actor-model runtime semantics without depending on ADK, Balda, Telegram, JetStream, NATS, MCP, queue providers, model providers, workspace policy, or product-specific task/projection lifecycles.

Actorlayer owns execution mechanics. Products own integration policy. A product such as Balda consumes actorlayer by translating product commands, sessions, task metadata, provider configuration, and delivery settlement into actorlayer refs, envelopes, deliveries, handlers, sources, sinks, and retry hooks.

## Package layout

- `pkg/actorlayer/`: core typed actor runtime.
- `pkg/actorlayer/dispatch/`: actor registration and address-based dispatch helpers.
- `pkg/actorlayer/engine/`: durable-delivery lane engine used by products that already own command queues.
- `pkg/actorlayer/persistence/`: persistence extension interfaces.
- `pkg/actoradapter/adk/`: optional Google ADK adapter for actor behaviors.

## Ownership contract

### Actorlayer owns

- Typed actor behaviors and one-envelope-at-a-time receive semantics.
- Addressable `Ref` values and local actor identifiers.
- `Envelope` as the mailbox transport unit.
- `Tell` and `Ask` message exchange.
- Per-actor FIFO mailboxes and mailbox backpressure policy.
- Lifecycle publication for actor start/stop events.
- Supervision directives and threshold supervision policy.
- Dead-letter sink mechanics for unhandled actor messages.
- Scheduling with `TellAfter`.
- Observer and tracing hooks.
- Provider-neutral durable-delivery engine contracts: `Delivery`, `Handler`, `Source`, `Resolver`, `EventSink`, `RetryPolicy`, and lane status.

### Adapter and product code owns

- Local worker, ADK-backed agent, or future provider execution setup.
- Provider credentials, model selection, sessions, tools, and runtime context.
- Command delivery transports such as JetStream, NATS, in-memory queues, or any future queue provider.
- Queue settlement: ack, retry, reject, deadletter, in-progress heartbeat, and max-delivery policy.
- Retry classification, backoff tuning, and retry exhaustion policy.
- Product event subjects, telemetry payloads, metrics naming, and operator status output.
- Task records, projection writes, DLQ storage, workspace policy, auth policy, and user-visible reporting.

These ownership boundaries are part of the public contract. Do not add product or provider dependencies to `pkg/actorlayer` or its engine packages.

## Core concepts

### Typed actors and behaviors

An actor is a behavior registered in a `System`. A behavior handles one `Envelope` at a time. Actor state is local to the behavior and protected by mailbox serialization.

### Addresses and references

A `Ref` is an addressable local reference to an actor in one `System`. Products may keep their own external addressing schemes, but those schemes must be resolved to actorlayer refs or dispatch addresses at the integration boundary.

### Envelopes

An `Envelope` carries message identity, correlation ID, sender, receiver, optional reply target, headers, payload, deadline, and send timestamp. Actorlayer does not prescribe product metadata names such as chat IDs, topic IDs, task IDs, provider IDs, or workspace IDs.

### Delivery engine

`pkg/actorlayer/engine` is for products that already have a durable command source. The product implements `Delivery` to expose envelope, attempt counts, in-progress heartbeat, ack, retry, and deadletter operations. The engine serializes delivery handling by lane key and calls product-supplied settlement hooks.

### Resolver and lanes

A `Resolver` maps each delivery to a deterministic lane key. Deliveries with the same lane key execute serially; different lanes may execute concurrently. The product decides which envelope fields form the lane key.

### Source and handler

A `Source` feeds deliveries to the engine. A `Handler` executes one delivery. The source may be backed by any transport, but transport APIs must stay outside actorlayer packages.

### Sink and runtime events

An `EventSink` receives generic runtime events: `running`, `in_progress`, `acked`, `retrying`, and `deadlettered`. Products map those generic events into product-specific subjects, payloads, logs, metrics, projections, or UI status.

### Retry policy hooks

`RetryPolicy` accepts product-supplied functions for retryability, backoff, and exhaustion. Actorlayer calls those hooks but does not define product retry rules.

### Lane status

`LaneStatus` reports active lane count and keys for operational visibility. Products decide how to expose that data in status commands, metrics, or dashboards.

## ADK adapter

`pkg/actoradapter/adk` adapts Google ADK agents to actorlayer behavior. It is optional adapter code, not an actorlayer dependency.

The ADK adapter owns ADK-specific mapping such as session/user policy, codecs, reply modes, and ADK tool exposure. It must not move ADK types into actorlayer contracts.

## Balda integration summary

Balda uses Norma actorlayer as the fixed actor execution engine and keeps product policy in Balda:

- Balda maps JetStream command messages into actorlayer engine deliveries.
- Balda owns `balda.provider`, ADK session/provider runtime setup, tools, and workspace context.
- Balda owns retry classification, backoff, retry exhaustion, task deadletter state, DLQ reporting, command telemetry, and SQLite projections.
- Balda command/status surfaces such as `/queue status`, `/dlq`, and `/projection status` expose Balda product semantics, not actorlayer policy.

## Quick example

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

## Scheduler and lifecycle example

```go
sys, _ := actorlayer.NewSystem(actorlayer.Config{})

observer, _ := sys.Spawn(ctx, "observer", actorlayer.Props{
	NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
		return actorlayer.ReceiveFunc(func(_ actorlayer.Context, env actorlayer.Envelope) error {
			if pub, ok := env.Payload.(actorlayer.PublishedMessage); ok && pub.Topic == actorlayer.LifecycleTopic {
				// Handle Started or Stopped events.
			}
			return nil
		}), nil
	},
})

_ = sys.Subscribe(actorlayer.LifecycleTopic, observer)
scheduled, _ := sys.TellAfter(ctx, 250*time.Millisecond, observer, "tick")
_ = <-scheduled.Done()
```

## Verification coverage

Actorlayer and adapter tests cover:

- sequential processing for one actor
- concurrency across actors
- mailbox full behavior under `FailFast`
- ask timeout
- watcher termination notification
- supervision panic path
- ADK runner invocation and reply mapping in `pkg/actoradapter/adk`
- session policy mapping in `pkg/actoradapter/adk`
- non-overlap for same ADK actor
- ADK toolset allow/deny, payload limits, and timeout limits
- drain shutdown behavior
- tell rate limiting
- observer and tracer span hooks
- actorlayer architecture checks that reject product/provider imports in core packages

## Known limitations

- Persistence package currently defines interfaces only; no concrete durable stores are included.
- Cluster/remote transport is not implemented.
- Lifecycle model is local-process only; remote lifecycle propagation is not implemented.
- Concrete OpenTelemetry or Prometheus adapters are not included; hooks are present.
