// Package acpagent adapts Agentic Computing Protocol (ACP) runtimes to the
// Google ADK [agent.Agent] interface.
//
// The package starts an ACP-compatible subprocess and maps each ADK session to
// a remote ACP session. By default, ACP session creation uses
// [Config.WorkingDir] as the ACP session cwd.
//
// # Per-session overrides via ADK state
//
// Callers can override ACP session creation per ADK session by setting
// [sessionstate.CWDKey] before the first invocation in that ADK session:
//
//	map[string]any{
//	  sessionstate.CWDKey: "/absolute/path", // optional
//	}
//
// ACP-specific session/new metadata may be provided under [SessionStateKey]:
//
//	map[string]any{
//	  acpagent.SessionStateKey: map[string]any{
//	    "session_id": "session-123", // optional; attempted via ACP session/resume
//	    "meta": map[string]any{ // optional; forwarded to ACP session/new _meta
//	      "codex": map[string]any{"approvalMode": "manual"},
//	    },
//	  },
//	}
//
// Behavior:
//   - If `state[sessionstate.CWDKey]` is set, it overrides [Config.WorkingDir]
//     for ACP session creation.
//   - If `state[SessionStateKey].session_id` is set, the runtime attempts
//     ACP session/resume first and falls back to session/new when resume is
//     unsupported or the referenced session no longer exists.
//   - If `state[SessionStateKey].meta` is set, it is passed through to ACP
//     session/new._meta and session/resume._meta.
//   - Overrides are read when the ACP session is first created for the ADK
//     session. Subsequent changes do not rebind that existing ACP session.
//
// Invalid override values (for example, non-string `state[sessionstate.CWDKey]`,
// non-object `state[SessionStateKey]`, non-object `state[SessionStateKey].meta`,
// non-string `state[SessionStateKey].session_id`,
// or a cwd that is not a valid existing directory) cause invocation failure
// before ACP session creation.
//
// # ACP plan updates
//
// ACP `session/update.plan` notifications are projected into ADK event state
// under [PlanStateKey]. Each event carries the full replacement snapshot at
// `event.Actions.StateDelta[PlanStateKey]` with an `entries` field containing
// the current ACP plan entries. Plan updates do not appear as content parts.
package acpagent
