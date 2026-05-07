# `pdca-taskmaster` playground

`pdca-taskmaster` is the PDCA-specific async wrapper over the shared Taskmaster async harness:

```bash
norma playground pdca-taskmaster "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge
```

It keeps the async queue/coordinator/runtime shape of Taskmaster, but the prompt contract is strict PDCA.

## What It Is

`pdca-taskmaster` boots:

- one root agent named `pdca-taskmaster`
- four fixed child agents:
  - `plan`
  - `do`
  - `check`
  - `act`

All of them are fixed to:

- agent type: `codex_acp`
- model: `gpt-5.3-codex`

This wrapper preserves the existing strict PDCA prompt contract:

- root coordinates `plan -> do -> check -> act`
- child prompts remain PDCA-specific
- child I/O is still plain text
- root interprets `check` and `act` semantically from plain text

## Control Surface

The control-tool namespace is the same as generic Taskmaster:

- `taskmaster.schedule_task`

Routing remains async and locator-based, but child locators are the PDCA child agents:

- `plan`
- `do`
- `check`
- `act`

Each child uses local agent address shape: `class=agent`, `transport=local`, `key=<role>`.

Default `report_to` resolves to the PDCA root agent:

```json
{
  "class": "agent",
  "transport": "local",
  "key": "pdca-taskmaster"
}
```

This wrapper accepts only agent-based `report_to` targets. It does not support the generic `integration/cli/log` report sink.

`taskmaster.schedule_task` also requires `session_id`. The root keeps the same session across PDCA turns for the same conversation.

`taskmaster.finish` still exists in this wrapper, but it is wrapper-owned. It is not part of the shared reusable `pkg/runtime/taskmaster` MCP package.

This wrapper is now built on the local reusable runtime packages:

- `pkg/runtime/taskmaster`
- `pkg/runtime/taskmaster/adk`
- `pkg/runtime/taskmaster/mcp`

## Relation to Other Playgrounds

- `taskmaster` = generic async harness with one `worker`
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA playground

Use [PDCA Loop](pdca-agent.md) for the real structured PDCA runtime behind `norma loop`.
