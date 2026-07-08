# `pdca-taskmaster` experimental command

`pdca-taskmaster` is the PDCA-specific async wrapper over the shared Taskmaster async harness:

```bash
norma pdca-taskmaster "ship the goal" \
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

The app-owned control-tool namespace in this wrapper is:

- `taskmaster.schedule_task`

Routing remains async, but the app-owned tool uses flat target names for the PDCA child nodes:

- `plan`
- `do`
- `check`
- `act`

The normal PDCA completion target is the PDCA root agent. Child outcomes are routed back to the root by the wrapper's runtime outcome policy.

This wrapper accepts flat local target names `plan`, `do`, `check`, and `act`. It does not expose the generic `integration/cli/log` sink.

`taskmaster.schedule_task` also requires `session_id`. The root keeps the same session across PDCA turns for the same conversation.

`taskmaster.finish` still exists in this wrapper, but it is wrapper-owned. The entire MCP surface is app-owned here; it is not part of shared `github.com/normahq/runtime/v2/taskmaster`.

This wrapper is now built on the local reusable runtime packages:

- `github.com/normahq/runtime/v2/taskmaster`
- `github.com/normahq/runtime/v2/taskmaster/adk`

Its app-level wiring is wrapper-owned:

- the wrapper builds `agent.Agent` instances with `agentfactory`
- the wrapper creates the MCP server instance and registers its own PDCA control tools
- `github.com/normahq/runtime/v2/taskmaster/adk` only wraps those built agents as local nodes

## Relation to Other Experimental Commands

- `taskmaster` = generic inbox runner
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA app

Use [PDCA Loop](pdca-agent.md) for the real structured PDCA runtime behind `norma loop`.
