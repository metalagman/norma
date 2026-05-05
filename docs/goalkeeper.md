# Goalkeeper Playground

Goalkeeper is an experimental playground feature with two temporary command surfaces:

- `norma playground goalkeeper <goal>`: synchronous scheduler-agent playground
- `norma playground goalkeeper-notify <goal>`: async queue-based playground

Goalkeeper is **PDCA-inspired**, not the structured PDCA contract implementation used by `norma loop`. The real PDCA runtime is documented in [PDCA Loop](pdca-agent.md).

## What Goalkeeper Is

Goalkeeper uses the role names `plan`, `do`, `check`, and `act`, but today it runs them as long-lived Codex ACP-backed agents with:

- fixed injected role instructions
- freeform per-job task text
- freeform text outputs
- debug lifecycle logging around job dispatch and completion

Goalkeeper does **not** currently implement:

- structured PDCA role JSON contracts
- real harness-level `continue` or `replan` semantics
- Beads integration
- the older fixed-DAG `depends_on` kernel model

All Goalkeeper agents are fixed to:

- agent type: `codex_acp`
- model: `gpt-5.3-codex`

`--bridge-bin` is optional and only overrides the executable used for the fixed Codex ACP bridge. When omitted, Goalkeeper uses:

```bash
npx -y @normahq/codex-acp-bridge@latest
```

Stdout is reserved for the final scheduler answer. Scheduler lifecycle messages use structured zerolog at info level. Per-job envelopes and role-agent output are debug-only.

## `goalkeeper`

The synchronous playground is a single scheduler turn backed by one MCP tool:

```bash
norma playground goalkeeper "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge \
  --max-tool-calls 8
```

Behavior:

- the scheduler receives one `GOAL JOB`
- the scheduler can run role work only through `goalkeeper.run_job`
- `goalkeeper.run_job` runs one prompt turn on exactly one role agent
- each role agent keeps its ADK session for the command lifetime
- the scheduler is instructed to keep the path simple: `plan -> do -> check -> act`

This command is a playground approximation of PDCA role orchestration. It is not the same thing as the structured PDCA loop in `norma loop`.

### `goalkeeper.run_job`

Tool input:

```json
{
  "job_id": "job-plan",
  "role": "plan",
  "task": "Plan how to satisfy the GOAL job."
}
```

Tool output:

```json
{
  "job_id": "job-plan",
  "role": "plan",
  "status": "completed",
  "result": "..."
}
```

The tool rejects unknown roles, empty job IDs, empty tasks, and calls beyond `--max-tool-calls`.

Example debug send envelope:

```json
{
  "level": "debug",
  "direction": "send",
  "job_id": "job-plan",
  "role": "plan",
  "task": "Plan how to satisfy the GOAL job.",
  "message": "job envelope"
}
```

Example debug receive envelope:

```json
{
  "level": "debug",
  "direction": "receive",
  "job_id": "job-plan",
  "role": "plan",
  "status": "completed",
  "result": "...",
  "message": "job envelope"
}
```

## `goalkeeper-notify`

The async playground keeps the same four role names, but changes the harness shape:

```bash
norma playground goalkeeper-notify "ship the goal" \
  --bridge-bin /path/to/codex-acp-bridge \
  --max-tool-calls 8
```

Current implementation:

- a code Coordinator owns scheduling
- every logical agent has an inbox queue
- one serial executor drains each inbox queue
- worker completions are reported back to the Coordinator
- the Coordinator creates scheduler notification jobs
- the scheduler reacts to those notifications with more scheduling or `goalkeeper.finish`

This is still a playground harness, not the structured PDCA runtime used by `norma loop`.

### Current limitations

- the exposed workflow is fixed to `plan -> do -> check -> act`
- worker job payloads are freeform text
- scheduler notifications are freeform text
- `act` may say `continue` or `replan` in prose, but those are not harness-level control states today
- the current harness finishes when the scheduler calls `goalkeeper.finish`
- jobs are in-memory only for this playground

### Async execution shape

```mermaid
flowchart TD
    Input[CLI goal] --> Coordinator
    Coordinator -->|enqueue| SchedulerQueue[Scheduler inbox queue]
    SchedulerQueue --> SchedulerExec[Scheduler executor]
    SchedulerExec --> SchedulerAgent[Scheduler agent]
    SchedulerAgent -->|goalkeeper.schedule_job| Coordinator
    Coordinator -->|enqueue worker job| WorkerQueue[Role inbox queue]
    WorkerQueue --> WorkerExec[Role executor]
    WorkerExec --> WorkerAgent[plan/do/check/act agent]
    WorkerAgent -->|job result| Coordinator
    Coordinator -->|enqueue notification job| SchedulerQueue
    SchedulerAgent -->|goalkeeper.finish| Coordinator
```

### `goalkeeper.schedule_job`

Tool input:

```json
{
  "job_id": "job-plan",
  "target_agent_id": "plan",
  "task": "Plan how to satisfy the GOAL job."
}
```

Tool output:

```json
{
  "job_id": "job-plan",
  "target_agent_id": "plan",
  "status": "queued",
  "message": "job queued"
}
```

### `goalkeeper.finish`

Tool input:

```json
{
  "summary": "Goal handled."
}
```

Tool output:

```json
{
  "status": "finished",
  "summary": "Goal handled."
}
```

Debug logs for `goalkeeper-notify` include:

- `job enqueued`
- `job started`
- `job completed`
- `job failed`
- `notification job created`
- `notification job received`

## Relation to Real PDCA

Use [PDCA Loop](pdca-agent.md) for the real contract and runtime semantics:

- structured `input.json -> output.json` role contracts
- lowercase control literals: `pass|fail`, `close|continue|replan`, `passed|failed|stopped`
- session-backed iterative PDCA loop behavior

Use Goalkeeper docs only for the experimental playground command surfaces.
