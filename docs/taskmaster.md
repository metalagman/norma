# `taskmaster` playground

Taskmaster is an experimental **generic async task harness** exposed as:

```bash
norma playground taskmaster \
  --bridge-bin /path/to/codex-acp-bridge

norma playground taskmaster "count total lines of the go files" \
  --bridge-bin /path/to/codex-acp-bridge

norma playground taskmaster-chat \
  --bridge-bin /path/to/codex-acp-bridge
```

This surface is workflow-agnostic. It is not the structured PDCA runtime used by `norma loop`, and it is not the PDCA-specific async wrapper. For those, see [PDCA Loop](pdca-agent.md) and [PDCA Taskmaster](pdca-taskmaster.md).

## What It Is

Generic `taskmaster` boots one root agent named `taskmaster`.

Both are fixed to:

- agent type: `codex_acp`
- model: `gpt-5.3-codex`

The runtime shape is queue-based:

- one inbox queue for the taskmaster agent
- one serial executor for that inbox
- host-owned message submission
- app-owned outcome routing
- optional multi-message emission while a node is running
- plain-text prompts and plain-text outputs

The root agent is workflow-agnostic. It processes the plain-text job messages that the host enqueues and keeps the run moving while the process stays alive.

Common root completion reporting target is:

```json
{
  "class": "agent",
  "transport": "local",
  "key": "taskmaster"
}
```

Generic `taskmaster` routes root outcomes to one app-owned sink:

```json
{
  "class": "integration",
  "transport": "cli",
  "key": "log"
}
```

Messages sent to that target are written to the current playground log and are **not** delivered back to the root agent.

## Runtime Flow

The generic flow is:

1. The playground starts the Taskmaster inbox runner.
2. If optional CLI content is provided, the wrapper enqueues one initial root job message.
   - the message content is passed through unchanged
3. The background timer may enqueue additional root job messages while the run is active.
4. The app-level outcome router converts root outcomes into notification/error messages for `integration/cli/log`.
5. The run stays active until the host context is canceled, typically by `SIGINT` or `SIGTERM`.
6. On shutdown, the playground stops gracefully.

Stdout remains reserved for:

- `Total run time: ...`

## Background Tasks

Generic `taskmaster` also runs a background timer. While the run is active, it periodically enqueues simple synthetic root tasks with raw `hello world` content.

These timer tasks are supplemental background traffic and continue until the command context is canceled.

## Fake Chat Playground

`taskmaster-chat` wraps the same single-agent runtime as a local fake chat harness:

- stdin is the ingress
- one fixed fake chat session id is reused for the whole process
- each non-empty line is enqueued unchanged to the root agent as a job message
- replies are routed to `human:fakechat:local` and rendered as:
  - `taskmaster> ...`
  - `you> ...`
- the background timer is disabled in this mode
- `/quit`, EOF, or process cancellation stops the run

## Reusable Runtime

The unstable reusable runtime now lives under:

- `pkg/runtime/taskmaster`
- `pkg/runtime/taskmaster/adk`

That local runtime provides:

- one public `Message` type with `id`, `session_id`, `kind`, `from`, `to`, optional `parent_id`, `content`, and optional `metadata`
- a `Node` interface that receives one message, can emit many addressed messages while running, and returns one terminal outcome
- session-aware local agent turns keyed by explicit `session_id`
- reusable locators with `class`, `transport`, `key`, and optional `address`
- app-owned outcome routing modeled as additional addressed messages
- lifecycle-style control: `New`, `Start`, `Stop`, `Done`, `Err`, `Enqueue`
- an ADK wrapper that turns an already-built `agent.Agent` into a `taskmaster.Node`

The app owns all app-level wiring:

- building `agent.Agent` instances with `agentfactory`
- creating any ingress protocol such as MCP, CLI bootstrap, or timers
- injecting the wrapped local nodes into `pkg/runtime/taskmaster`

## Relation to Other Playgrounds

- `taskmaster` = generic async harness
- `pdca-taskmaster` = async PDCA wrapper over the same harness core
- `pdca-sync` = sync PDCA playground
