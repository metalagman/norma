# `goalkeeper` playground

Run:

```bash
norma playground goalkeeper "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge
```

`goalkeeper` is an experimental ADK workflow playground for one prompt-driven work pass followed by validation.

It boots:
- one ADK sequential workflow agent named `Goalkeeper`
- one worker child agent named `GoalkeeperWorker`
- one validator child agent named `GoalkeeperValidator`

The workflow is fixed:
- the worker receives the goal and performs the requested work in the current working directory
- the validator runs after the worker and validates the result against the same goal

Both child agents use:
- ACP-backed ADK agents
- the same resolved current working directory
- the same ADK runner session for the workflow turn

This playground does not use:
- Taskmaster queues
- PDCA phase agents
- structured PDCA JSON contracts
- retry or loop control

## Prompt Contract

The CLI arguments become one goal prompt:

```text
Goal:
<goal>
```

The worker returns a concise plain-text summary and evidence. The validator should start its final response with either:

```text
verdict: pass
```

or:

```text
verdict: fail
```

Validation is report-only in this playground. A failed verdict is printed for the human/operator to inspect; the command does not parse the verdict or retry automatically.

The runtime also emits structured zerolog events for:
- workflow start and completion
- worker step start and completion
- validator step start and completion
- validation completion with `verdict` and `goal_reached`

`goal_reached=true` is logged only when the validator response starts with `verdict: pass`. `verdict: fail`, missing verdicts, and malformed verdicts are logged as not reached without changing command exit behavior.

## Relation to Other Playgrounds

- `taskmaster` = generic async inbox runner
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA coordinator wrapper
- `goalkeeper` = deterministic worker -> validator sequence
