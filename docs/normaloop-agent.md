# NormaLoop Agent In `norma loop`

This document describes how the loop command orchestrates tasks with the PDCA workflow agent.

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
  - workflow: `pdca.NewWorkflow(...)`
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

- Check step writes verdict into session state key `verdict`: `internal/workflows/pdca/agent.go`
- Act step writes decision into session state key `decision`: `internal/workflows/pdca/agent.go`

Workflow finalization:

- Reads `verdict` + `decision` from session with fallback from `task_state`
- derives final status/verdict (`passed|failed|stopped`)
- persists final run status to DB

Code: `internal/workflows/pdca/workflow.go`

### 6) Perform verdict change

After workflow returns:

- If verdict is `PASS`:
  - apply workspace changes to main repo
  - create commit
  - mark task `done` in Beads
  - code: `internal/run/run.go`
- For non-pass outcomes:
  - returns failed/stopped status to caller
  - loop may continue if `--continue` is set
  - code: `cmd/norma/task.go`

### 7) Pick next task

- `runTasks(...)` loops back to `tracker.List(todo)` and repeats until no tasks or failure stop condition.

## Data Boundaries

- Beads (`bd`): task/backlog source of truth
- SQLite (`.norma/norma.db`): run/step/event state
- Filesystem (`.norma/runs/...`): per-step artifacts/logs/workspaces

## Related Files

- `cmd/norma/loop.go`
- `cmd/norma/task.go`
- `internal/task/beads_tracker.go`
- `internal/task/scheduler.go`
- `internal/run/run.go`
- `internal/workflows/pdca/agent.go`
- `internal/workflows/pdca/workflow.go`
