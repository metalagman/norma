// Package structuredagent wraps ADK agents with JSON-schema-constrained I/O.
//
// [NewAgent] validates structured input before invoking the wrapped agent and
// validates structured output before returning the final ADK event. The package
// is intended for higher-level workflows that require deterministic machine-
// readable JSON instead of free-form model text.
package structuredagent
