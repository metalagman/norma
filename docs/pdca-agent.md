# PDCA Loop

This document describes Norma's fixed execution loop:

`plan -> do -> check -> act`

The loop repeats until the task is completed (`PASS` + `act.decision=close`) or a stop condition is reached.

## Scope

- Single workflow only: no alternative orchestration graph.
- One task at a time per run.
- Each step is contract-driven: `input.json -> output.json`.

## Iteration Flow

### 1) Plan

Purpose:
- refine the selected task into an executable plan for this iteration
- define effective acceptance criteria and verification checks

Expected output:
- `plan_output.acceptance_criteria.effective`
- `plan_output.work_plan.do_steps`
- `plan_output.work_plan.check_steps`

### 2) Do

Purpose:
- execute only planned implementation steps
- produce artifacts and code changes inside step workspace

Expected output:
- `do_output.execution.executed_step_ids`
- `do_output.execution.skipped_step_ids`

### 3) Check

Purpose:
- verify plan-vs-execution match
- verify effective acceptance criteria
- produce verdict

Expected output:
- `check_output.acceptance_results`
- `check_output.verdict.status` (`PASS|FAIL|PARTIAL`)

Verdict rules:
- Any failed acceptance result -> `FAIL`
- Otherwise verdict is derived by the Check agent and recorded in `check_output.verdict`.

Current schema note:
- `check_output.plan_match` is not part of the current `check/output.schema.json`.
- `do_output.execution.commands` is not part of the current `do/output.schema.json`.

### 4) Act

Purpose:
- decide what happens next from check verdict

Expected output:
- `act_output.decision` (`close|replan|rollback|continue`)

Act behavior:
- `close` with effective `PASS`: task is closed and changes are applied to main repo.
- `replan|continue|rollback`: task remains open and loop may continue or stop by policy.

## Workspaces and Artifacts

- Every step runs in its own step directory:
  - `.norma/runs/<run_id>/steps/<NNN-role>/`
- Every step has isolated git worktree:
  - `<step_dir>/workspace`
- Step files:
  - `input.json`
  - `output.json`
  - `logs/stdout.txt`
  - `logs/stderr.txt`
  - `artifacts/progress.md`

## State Model

Authoritative state split:
- Backlog/task state: Beads (`bd`) + task `notes` (`TaskState` JSON)
- Run/step timeline: SQLite (`.norma/norma.db`)
- Human-readable artifacts: filesystem under `.norma/runs/`

`TaskState` persists:
- latest Plan/Do/Check/Act outputs
- run journal entries used to reconstruct `artifacts/progress.md`

## Stop Conditions

Norma stops the loop when any applies:
- budget exceeded
- dependency blocked
- verification missing/unrunnable
- replan required
- explicit terminal decision (`act.decision=close`)

Stop reason must be represented in step output (`status=stop` with concrete `stop_reason`) when applicable.

## Provider Routing Policy

- CLI providers (`codex`, `opencode`, `gemini`, `claude`) run via ADK `exec` + `ainvoke`.
- OpenAI provider runs as ADK-native `type: openai` (not via `ainvoke`).

## References

- Canonical workflow and contracts: `AGENTS.md`
- Schemas:
  - `internal/workflows/pdca/plan/*.schema.json`
  - `internal/workflows/pdca/do/*.schema.json`
  - `internal/workflows/pdca/check/*.schema.json`
  - `internal/workflows/pdca/act/*.schema.json`
