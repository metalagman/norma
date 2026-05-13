# `goalkeeper` playground

Run:

```bash
norma playground goalkeeper "ship the goal" \
  --max-iterations 5 \
  --bridge-bin /path/to/codex-acp-bridge
```

`goalkeeper` is an experimental ADK workflow playground for one prompt-driven work loop followed by validation.

It boots:
- one ADK LoopAgent workflow agent named `Goalkeeper`
- one worker child agent named `GoalkeeperWorker`
- one validator child agent named `GoalkeeperValidator`

The workflow is fixed:
- the worker receives the goal and performs the requested work in the current working directory
- the validator runs after the worker and validates the result against the same goal
- if the validator starts its final response with `verdict: pass`, the loop exits
- otherwise the worker and validator retry until `--max-iterations` is exhausted

Both child agents use:
- ACP-backed ADK agents
- the same resolved current working directory
- the same ADK runner session for the workflow turn

This playground does not use:
- Taskmaster queues
- PDCA phase agents
- structured PDCA JSON contracts

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

The command always prints the final validator output and total runtime. A passing verdict exits with status 0. A failed or missing verdict exits non-zero after printing the final evidence.

The runtime also emits structured zerolog events for:
- workflow start and completion
- worker step start and completion
- validator step start and completion
- validation completion with `verdict` and `goal_reached`

The ADK workflow stream also includes metadata-only `session.Event` records around each worker and validator step. These events have no `Content`, are persisted in ADK session history, and identify `step_started`, `step_completed`, or `step_failed` in `CustomMetadata["norma.goalkeeper.event"]`.

When `--debug` is enabled, the runtime also logs one `goalkeeper model final text` event per child step with the step name and the complete final visible model text. It does not log partial chunks or thought parts.

`goal_reached=true` is logged only when the validator response starts with `verdict: pass`. `verdict: fail`, missing verdicts, and malformed verdicts are logged as not reached and produce a non-zero exit status.

## Relation to Other Playgrounds

- `taskmaster` = generic async inbox runner
- `pdca-taskmaster` = async PDCA wrapper
- `pdca-sync` = sync PDCA coordinator wrapper
- `goalkeeper` = deterministic worker -> validator loop
