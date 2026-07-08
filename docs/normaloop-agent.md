# NormaLoop Agent In `norma loop`

This document describes how the loop command orchestrates tasks with the PDCA workflow agent.

`norma loop` is the core strict PDCA path for one selected task at a time. The swarm harness is an experimental non-core surface pending a product support decision.

## Config env substitution

`norma loop` uses the same config env substitution behavior as the rest of norma config loading.
Both `$VAR` and `${VAR}` placeholders are supported (envsubst-style), and substitution is evaluated during config load before YAML parsing.

Example:

```yaml
runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
```

If any referenced variable is missing, config expansion fails and reports the missing variable name(s).

## What It Does

For each cycle, `norma loop`:

1. reads candidate tasks from Beads (`bd`)
2. selects one task
3. runs the PDCA workflow on that task
4. checks verdict/decision from PDCA session state (inside workflow finalization)
5. applies verdict effects (DB status, git apply on pass, task close on pass)
6. picks the next task and repeats

## Control Flow

### 1) Loop setup

- CLI command: `cmd/norma/loop/command.go`
- Creates:
  - Beads tracker: `task.NewBeadsTracker("")`
  - run store: `db.NewStore(...)`
  - PDCA agent factory: `pdca.NewFactory(...)`
  - normaloop loop agent: `normaloop.New(...)`

### 2) Read tasks from Beads

- The selector agent loads ready tasks through `task.SelectNextReady(...)`.
- Tracker maps Norma status filters onto Beads status commands in `internal/task/beads_tracker.go`.

### 3) Pick one task

- Scheduler selection: `task.SelectNextReady(...)`: `internal/task/scheduler.go`
- Uses policy filters, leaf preference, and priority/tie-breakers.

### 4) Run PDCA on selected task

- `runTaskByID(...)` calls the configured PDCA agent factory from `internal/agents/normaloop/task.go`.
- The factory creates the run record in `.norma/norma.db` then executes the structured PDCA workflow.

### 5) Verdict from PDCA agent session

Inside PDCA agent execution:

- Check step writes verdict into session state key `verdict`: `internal/agents/pdca/agent.go`
- Act step writes decision into session state key `decision`: `internal/agents/pdca/agent.go`
- Verdict literals are lowercase: `pass`, `fail`
- Decision literals are lowercase: `close`, `continue`, `replan`
- Run status literals are lowercase: `passed`, `failed`, `stopped`

Workflow finalization:

- Reads `verdict` + `decision` from session with fallback from the live ADK session `task_state`
- derives final status/verdict (`passed|failed|stopped`)
- persists final run status to DB

Code: `internal/agents/pdca/factory.go`

### 6) Perform verdict change

After workflow returns:

- If `verdict=pass` + `decision=close`:
  - apply workspace changes to main repo
  - create commit
  - mark task `done` in Beads
  - code: `internal/run/run.go`
- If `verdict=fail` + `decision=continue`:
  - `continue` is handled inside the PDCA ADK loop by advancing the in-session `iteration`
  - the loop keeps running the same selected task until it reaches a terminal outcome or a stop condition
  - there is no separate normaloop retry queue or in-memory retry backoff path documented in the current implementation
- If `verdict=fail` + `decision=replan`:
  - create/link replacement work
  - close the old task with a replan reason
  - return final status `failed`
- Invalid combinations such as `pass` + `continue`, `pass` + `replan`, `fail` + `close`, or any `rollback` decision are agent contract violations.

### 7) Pick next task

- `norma loop` keeps running after task-run, tracker, DB, agent, finalize, and apply failures once startup has succeeded.
- Runtime failures are logged and backed off instead of stopping the CLI process.
- Startup/preflight failures still stop the command before the loop starts.

## Data Boundaries

- Beads (`bd`): task/backlog source of truth
- SQLite (`.norma/norma.db`): run/step/event state
- Filesystem (`.norma/runs/...`): per-step logs/artifacts plus one shared run workspace

## Related Files

- `cmd/norma/loop/command.go`
- `cmd/norma/run/helpers.go`
- `internal/task/beads_tracker.go`
- `internal/task/scheduler.go`
- `internal/run/run.go`
- `internal/agents/pdca/agent.go`
- `internal/agents/pdca/factory.go`
- `internal/agents/pdca/runner.go`
