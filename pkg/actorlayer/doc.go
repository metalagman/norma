// Package actorlayer provides provider-agnostic typed actor execution
// primitives.
//
// Actorlayer is product-agnostic infrastructure. It does not depend on Google
// ADK, Balda, Telegram, JetStream, NATS, MCP, model providers, workspace
// policy, queue providers, or product-specific task/projection lifecycles.
// Those concerns belong to adapter and product integration packages.
//
// The package owns local actor mechanics: addressable refs, typed envelopes,
// per-actor mailboxes, Tell and Ask messaging, lifecycle publication,
// supervision, dead-letter sinks, scheduling, observer hooks, and tracing
// hooks. A product embeds actorlayer by translating its own commands and
// runtime context into actorlayer refs, envelopes, and behaviors.
//
// Runtime providers and transports are intentionally outside this package.
// Local workers, ADK-backed agents, command delivery, ack/retry/deadletter
// settlement, telemetry shaping, and persistence side effects must be adapted
// at the boundary instead of being embedded in actorlayer core.
package actorlayer
