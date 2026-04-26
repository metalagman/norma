# NormaLoop Agent In `norma loop`

This document describes how the loop command orchestrates tasks with the PDCA workflow agent.

## Config env substitution

`norma loop` uses the same config env substitution behavior as the rest of norma config loading.
Both `$VAR` and `${VAR}` placeholders are supported (envsubst-style), and substitution is evaluated during config load before YAML parsing.

Example:

```yaml
norma:
  agents:
    gemini_acp:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
```

If any referenced variable is missing, config expansion fails and reports the missing variable name(s).

## What It Does

For each cycle, `norma loop`:

1. reads candidate tasks from Beads (`bd`)
2. selects one task
3. runs the PDCA workflow on that task
4. checks verdict/decision from PDCA session state (inside workflow finalization)
5. applies verdict effects (DB status, git apply on PASS, task close on PASS)
6. picks the next task and repeats

## Control Flow

### 1) Loop setup

- CLI command: `cmd/norma/loop.go`
- Creates:
  - Beads tracker: `task.NewBeadsTracker("")`
  - run store: `db.NewStore(...)`
  - PDCA agent factory: `pdca.NewFactory(...)`
  - normaloop loop agent: `normaloop.NewLoop(...)`
  - runner: `run.NewADKRunner(...)`

### 2) Read tasks from Beads

- `runTasks(...)` loads tasks with status `todo`: `cmd/norma/task.go`
- Tracker maps `todo` -> `bd list --status open`: `internal/task/beads_tracker.go`

### 3) Pick one task

- Scheduler selection: `task.SelectNextReady(...)`: `internal/task/scheduler.go`
- Uses policy filters, leaf preference, and priority/tie-breakers.

### 4) Run PDCA on selected task

- `runTaskByID(...)` calls `runner.Run(...)`: `cmd/norma/task.go`
- Runner creates run record in `.norma/norma.db` then executes workflow: `internal/run/run.go`

### 5) Verdict from PDCA agent session

Inside PDCA agent execution:

- Check step writes verdict into session state key `verdict`: `internal/agents/pdca/agent.go`
- Act step writes decision into session state key `decision`: `internal/agents/pdca/agent.go`
- Verdict literals are uppercase: `PASS`, `FAIL`
- Decision literals are lowercase: `close`, `continue`, `replan`
- Run status literals are lowercase: `passed`, `failed`, `stopped`

Workflow finalization:

- Reads `verdict` + `decision` from session with fallback from the live ADK session `task_state`
- derives final status/verdict (`passed|failed|stopped`)
- persists final run status to DB

Code: `internal/agents/pdca/factory.go`

### 6) Perform verdict change

After workflow returns:

- If `verdict=PASS` + `decision=close`:
  - apply workspace changes to main repo
  - create commit
  - mark task `done` in Beads
  - code: `internal/run/run.go`
- If `verdict=FAIL` + `decision=continue`:
  - keep the task open
  - schedule an in-memory retry backoff for that task
  - keep selecting other ready tasks while the retry backoff is active
- If `verdict=FAIL` + `decision=replan`:
  - create/link replacement work
  - close the old task with a replan reason
  - return final status `failed`
- Invalid combinations such as `PASS` + `continue`, `PASS` + `replan`, `FAIL` + `close`, or any `rollback` decision are agent contract violations.

### 7) Pick next task

- `norma loop` keeps running after task-run, tracker, DB, agent, finalize, and apply failures once startup has succeeded.
- Runtime failures are logged and backed off instead of stopping the CLI process.
- Failed tasks can be retried after their in-memory retry backoff expires.
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
