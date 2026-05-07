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

The root agent is workflow-agnostic. It schedules work to `worker`, interprets plain-text completions, and keeps emitting its current best plain-text summary while the run stays active.

## Control Surface

The MCP control namespace is:

- `taskmaster.schedule_task`

`schedule_task` still accepts:

- `task_id`
- `session_id`
- `locator`
- optional `report_to`
- `prompt`

The only child locator in this wrapper is:

```json
{
  "type": "agent",
  "kind": "local",
  "id": "worker"
}
```

Default completion reporting is:

```json
{
  "type": "agent",
  "kind": "local",
  "id": "taskmaster"
}
```

Generic `taskmaster` also supports one special `report_to` sink:

```json
{
  "type": "sink",
  "kind": "human_output",
  "id": "current_log"
}
```

If a child task uses that target, its completion is written to the current playground log and is **not** delivered back to the root agent.

## Runtime Flow

The generic flow is:

1. CLI enqueues the initial goal to the root `taskmaster` agent.
2. Root calls `taskmaster.schedule_task` with the current `session_id` to hand work to `worker`.
3. `worker` runs a plain-text prompt as a black box.
4. Completion is either:
   - routed back to root through `report_to = agent`, or
   - written to the log through `report_to = sink/human_output`
5. The run stays active until the host context is canceled, typically by `SIGINT` or `SIGTERM`.
6. On shutdown, the playground returns the latest plain-text output produced by the root agent.

Stdout remains reserved for:

- final root summary
- `Total run time: ...`

## Background Goals

Generic `taskmaster` also runs a background timer. While the run is active, it periodically enqueues simple synthetic root goals with `hello world` prompt text.

These timer goals are supplemental to the initial user goal and continue until the command context is canceled.

## Reusable Core Notes

The shared `taskmastercore` now supports:

- root-only mode with zero child agents
- session-aware agent turns keyed by explicit `session_id`
- reusable locators with `type`, `kind`, and `id`
- app-owned provider routing for non-local report or target addresses
- a separate root-ingress API for external integrations

## Relation to Other Playgrounds

- `taskmaster` = generic async harness
- `pdca-taskmaster` = async PDCA wrapper over the same harness core
- `pdca-sync` = sync PDCA playground
