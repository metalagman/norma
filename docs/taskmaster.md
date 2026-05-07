# `taskmaster` playground

Taskmaster is an experimental **generic async task harness** exposed as:

```bash
norma playground taskmaster "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge
```

This surface is workflow-agnostic. It is not the structured PDCA runtime used by `norma loop`, and it is not the PDCA-specific async wrapper. For those, see [PDCA Loop](pdca-agent.md) and [PDCA Taskmaster](pdca-taskmaster.md).

## What It Is

Generic `taskmaster` boots:

- one root agent named `taskmaster`
- one child worker agent named `worker`

Both are fixed to:

- agent type: `codex_acp`
- model: `gpt-5.3-codex`

The runtime shape is still queue-based:

- one inbox queue per agent
- one serial executor per inbox
- coordinator-owned task scheduling
- locator-based child routing
- completion routing through `report_to`
- plain-text prompts and plain-text outputs

The root agent is workflow-agnostic. It schedules work to `worker`, interprets plain-text follow-up tasks, and keeps the run moving while the process stays alive.

## Control Surface

The MCP control namespace is:

- `taskmaster.schedule_task`

`schedule_task` accepts:

- `task_id`
- `session_id`
- `locator`
- optional `report_to`
- `content`

The only child locator in this wrapper is:

```json
{
  "class": "agent",
  "transport": "local",
  "key": "worker"
}
```

Default completion reporting is:

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

If a child task uses that target, its completion is written to the current playground log and is **not** delivered back to the root agent.

## Runtime Flow

The generic flow is:

1. CLI enqueues the initial task content to the root `taskmaster` agent.
   - source locator: `integration/cli/input`
2. Root calls `taskmaster.schedule_task` with the current `session_id` to hand work to `worker`.
3. `worker` runs plain-text task content as a black box.
4. Completion is either:
   - routed back to root through `report_to = agent`, or
   - written to the log through `report_to = integration/cli/log`
5. The run stays active until the host context is canceled, typically by `SIGINT` or `SIGTERM`.
6. On shutdown, the playground stops gracefully.

Stdout remains reserved for:

- `Total run time: ...`

## Background Goals

Generic `taskmaster` also runs a background timer. While the run is active, it periodically enqueues simple synthetic root tasks with `hello world` content.

Those synthetic goals use source locator `integration/timer/default`.

These timer goals are supplemental to the initial user goal and continue until the command context is canceled.

## Reusable Runtime

The unstable reusable runtime now lives under:

- `pkg/runtime/taskmaster`
- `pkg/runtime/taskmaster/adk`
- `pkg/runtime/taskmaster/mcp`

That local runtime provides:

- one public `Task` type with `content`
- session-aware local agent turns keyed by explicit `session_id`
- reusable locators with `class`, `transport`, `key`, and optional `address`
- completion routing modeled as another task addressed to `report_to`
- lifecycle-style control: `New`, `Start`, `Stop`, `Done`, `Err`, `Enqueue`
- shared MCP tooling only for `taskmaster.schedule_task`

## Relation to Other Playgrounds

- `taskmaster` = generic async harness
- `pdca-taskmaster` = async PDCA wrapper over the same harness core
- `pdca-sync` = sync PDCA playground
