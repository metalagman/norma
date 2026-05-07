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
- `taskmaster.finish`

Routing remains async and locator-based, but child locators are the PDCA child agents:

- `plan`
- `do`
- `check`
- `act`

Default `report_to` resolves to the PDCA root agent:

```json
{
  "type": "agent",
  "id": "pdca-taskmaster"
}
```

This wrapper accepts only agent-based `report_to` targets. It does not support the generic `human_output` report sink.

## Relation to Other Playgrounds

- `taskmaster` = generic async harness with one `worker`
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA playground

Use [PDCA Loop](pdca-agent.md) for the real structured PDCA runtime behind `norma loop`.
