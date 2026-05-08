# `taskmaster` playground

Taskmaster is an experimental **generic async task harness** exposed as:

```bash
norma playground taskmaster \
  --bridge-bin /path/to/codex-acp-bridge

norma playground taskmaster "count total lines of the go files" \
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
- host-owned task submission
- completion routing through `report_to`
- plain-text prompts and plain-text outputs

The root agent is workflow-agnostic. It processes the plain-text tasks that the host enqueues and keeps the run moving while the process stays alive.

Common root completion reporting target is:

```json
{
  "class": "agent",
  "transport": "local",
  "key": "taskmaster"
}
```

Generic `taskmaster` also supports one special `report_to` sink:

```json
{
  "class": "integration",
  "transport": "cli",
  "key": "log"
}
```

If a task uses that target, its completion is written to the current playground log and is **not** delivered back to the root agent.

## Runtime Flow

The generic flow is:

1. The playground starts the Taskmaster inbox runner.
2. If optional CLI content is provided, the wrapper enqueues one initial root task.
   - the task payload is passed through unchanged
3. The background timer may enqueue additional root tasks while the run is active.
4. If `report_to` is set, completion is delivered as another task to that locator.
   - for example, route back to root with `report_to = agent/local/taskmaster`
   - or write to the log with `report_to = integration/cli/log`
   - if `report_to` is omitted, the task is fire-and-forget
5. The run stays active until the host context is canceled, typically by `SIGINT` or `SIGTERM`.
6. On shutdown, the playground stops gracefully.

Stdout remains reserved for:

- `Total run time: ...`

## Background Tasks

Generic `taskmaster` also runs a background timer. While the run is active, it periodically enqueues simple synthetic root tasks with raw `hello world` content.

These timer tasks are supplemental background traffic and continue until the command context is canceled.

## Reusable Runtime

The unstable reusable runtime now lives under:

- `pkg/runtime/taskmaster`
- `pkg/runtime/taskmaster/adk`

That local runtime provides:

- one public `Task` type with `session_id`, `locator`, optional `report_to`, and `content`
- session-aware local agent turns keyed by explicit `session_id`
- reusable locators with `class`, `transport`, `key`, and optional `address`
- completion routing modeled as another task addressed to `report_to`
- lifecycle-style control: `New`, `Start`, `Stop`, `Done`, `Err`, `Enqueue`
- an ADK wrapper that turns an already-built `agent.Agent` into a `taskmaster.LocalRunner`

The app owns all app-level wiring:

- building `agent.Agent` instances with `agentfactory`
- creating any ingress protocol such as MCP, CLI bootstrap, or timers
- injecting the wrapped local runners into `pkg/runtime/taskmaster`

## Relation to Other Playgrounds

- `taskmaster` = generic async harness
- `pdca-taskmaster` = async PDCA wrapper over the same harness core
- `pdca-sync` = sync PDCA playground
