// Package engine provides a provider-neutral durable-delivery runtime core for
// actorlayer dispatch.
//
// The engine contract is deliberately small. A product supplies a Source that
// yields deliveries, a Handler that executes one delivery, a Resolver that maps
// each delivery to a deterministic lane key, an optional EventSink for generic
// lifecycle events, and RetryPolicy hooks for retry classification, backoff,
// and exhaustion checks.
//
// Engine events are generic execution facts: running, in_progress, acked,
// retrying, and deadlettered. The engine does not define product event subjects,
// queue names, task state transitions, DLQ storage, projection writes, provider
// selection, ADK sessions, Telegram delivery, MCP tools, model routing, or
// workspace policy. Products such as Balda own those decisions in their
// integration layer while using this package for deterministic lane execution
// and delivery settlement orchestration.
package engine
