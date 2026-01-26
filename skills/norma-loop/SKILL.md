---
name: norma-loop
description: Operational guide for running the Norma PDCA lifecycle (plan/do/check/act) with strict JSON input/output contracts, pre-created workspace, orchestrator-applied commits, budgets, plan→do→check→act obligations, per-step stdout/stderr logs, acceptance criteria extension in Plan, plan-vs-actual verification in Check, and a Ralph-style run journal in artifacts/progress.md.
---

# norma-loop

Run the **single fixed** Norma workflow: **Plan → Do → Check → Act** until **PASS** or a **stop condition** triggers.

This document defines operational contracts. For the authoritative full spec, read `AGENTS.md` at the repo root before making changes.

## Core invariants

1. **One workflow only:** `plan -> do -> check -> act` (repeat).
2. **Workspace exists before any agent runs:** the orchestrator creates `runs/<run_id>/workspace/` before Plan.
3. **Agents never modify workspace or git:** all agents operate in **read-only** mode with respect to `workspace/`.
4. **Commits/changes happen outside agents:** the orchestrator may apply patches and create commits **outside agent execution**.
5. **Contracts are JSON only:** every step is `input.json → output.json`.
6. **Every step captures logs:**
    - `steps/<n>-<role>/logs/stdout.txt`
    - `steps/<n>-<role>/logs/stderr.txt`
7. **Run journal:** the orchestrator appends one entry after every step to the task's `Journal` state and `artifacts/progress.md`.
8. **Acceptance criteria (AC):** baseline ACs are passed into Plan; Plan may extend them with traceability.
9. **Check compares plan vs actual and verifies job done:** Check must compare the Plan work plan to Do execution and evaluate all effective ACs.
10. **Verdict goes to Act:** Act receives Check verdict and decides next.
11. **State Persistence:** The task's `notes` field stores a structured JSON `TaskState`. `artifacts/progress.md` is reconstructed from this state on run start.

## Directory layout (must exist)

```
runs/<run_id>/
  workspace/                          # created by orchestrator before any agent run
  artifacts/
    progress.md                        # append-only journal (orchestrator-written)
  steps/
    01-plan/
      input.json
      output.json
      logs/
        stdout.txt
        stderr.txt
    02-do/
      input.json
      output.json
      logs/
        stdout.txt
        stderr.txt
    03-check/
      input.json
      output.json
      logs/
        stdout.txt
        stderr.txt
    04-act/
      input.json
      output.json
      logs/
        stdout.txt
        stderr.txt
```

### Write rules (strict)

- Agents may write **only** inside their own step directory:
    - `steps/<n>-<role>/input.json`
    - `steps/<n>-<role>/output.json`
    - `steps/<n>-<role>/logs/stdout.txt`
    - `steps/<n>-<role>/logs/stderr.txt`
- The orchestrator writes:
    - `workspace/` (create, apply changes, commit, etc. outside agents)
    - `artifacts/progress.md` (append-only)

## Task IDs

- `task.id` must match: `^norma-[a-z0-9]+$`
    - examples: `norma-a3f2dd`, `norma-01`, `norma-fixlogin2`
- Non-matching IDs → Plan must stop with `stop_reason="replan_required"` (reason in logs).

## Labels

- `norma-has-plan`: Present if a valid work plan exists in task notes. Skips Plan step.
- `norma-has-do`: Present if work has been implemented in the workspace. Skips Do step.
- `norma-has-check`: Present if a verdict has been produced. Skips Check step.

## Budgets and stop conditions

The orchestrator must stop immediately when any applies:
- budget exceeded (iterations / wall time / failed checks / retries)
- dependency blocked
- verify missing (verification cannot run as planned)
- replan required (Plan cannot produce a safe/complete work plan)

Stopping must be reflected in `output.json` with `status="stop"` and a concrete `stop_reason`.

## Contracts

### Common `input.json` (all steps)

```json
{
  "run": {
    "id": "r-...",
    "iteration": 1
  },
  "task": {
    "id": "norma-a3f2dd",
    "title": "...",
    "description": "...",
    "acceptance_criteria": [
      { "id": "AC-1", "text": "...", "verify_hints": ["..."] }
    ]
  },
  "step": {
    "index": 1,
    "name": "plan|do|check|act",
    "dir": "runs/<run_id>/steps/<n>-<role>"
  },
  "paths": {
    "workspace_dir": "runs/<run_id>/workspace",
    "workspace_mode": "read_only"
  },
  "budgets": {
    "max_iterations": 5,
    "max_wall_time_minutes": 30,
    "max_failed_checks": 2
  },
  "stop_reasons_allowed": [
    "budget_exceeded",
    "dependency_blocked",
    "verify_missing",
    "replan_required"
  ],
  "context": {
    "facts": {},
    "links": []
  }
}
```

### Common `output.json` (all steps)

```json
{
  "status": "ok|stop|error",
  "stop_reason": "none|budget_exceeded|dependency_blocked|verify_missing|replan_required",
  "summary": {
    "text": "short human summary",
    "warnings": [],
    "errors": []
  },
  "logs": {
    "stdout_path": "steps/<n>-<role>/logs/stdout.txt",
    "stderr_path": "steps/<n>-<role>/logs/stderr.txt"
  },
  "timing": {
    "wall_time_ms": 0
  },
  "progress": {
    "title": "short line for the run journal",
    "details": [
      "bullet 1",
      "bullet 2"
    ],
    "links": {
      "stdout": "steps/<n>-<role>/logs/stdout.txt",
      "stderr": "steps/<n>-<role>/logs/stderr.txt"
    }
  }
}
```

## Step requirements (must)

### 01-plan requirements

Plan **must**:
- produce `work_plan` (the iteration plan)
- publish `acceptance_criteria.effective` (may extend baseline with traceability)

Plan `output.json` must include:

```json
{
  "plan": {
    "task_id": "norma-a3f2dd",

    "goal": "what success means for this iteration",
    "constraints": ["..."],

    "acceptance_criteria": {
      "baseline": [
        { "id": "AC-1", "text": "...", "verify_hints": ["..."] }
      ],
      "effective": [
        {
          "id": "AC-1",
          "origin": "baseline",
          "text": "Unit tests pass",
          "checks": [
            { "id": "CHK-AC-1-1", "cmd": "go test ./...", "expect_exit_codes": [0] }
          ]
        },
        {
          "id": "AC-1.1",
          "origin": "extended",
          "refines": ["AC-1"],
          "text": "No skipped tests",
          "checks": [
            { "id": "CHK-AC-1.1-1", "cmd": "go test ./... -run .", "expect_exit_codes": [0] }
          ],
          "reason": "Clarifies ambiguous AC-1."
        }
      ]
    },

    "work_plan": {
      "timebox_minutes": 30,

      "do_steps": [
        {
          "id": "DO-1",
          "text": "Run unit tests",
          "commands": [
            { "id": "CMD-1", "cmd": "go test ./...", "expect_exit_codes": [0] }
          ],
          "targets_ac_ids": ["AC-1", "AC-1.1"]
        }
      ],

      "check_steps": [
        {
          "id": "VER-1",
          "text": "Evaluate effective acceptance criteria",
          "mode": "acceptance_criteria"
        }
      ],

      "stop_triggers": [
        "dependency_blocked",
        "verify_missing",
        "budget_exceeded",
        "replan_required"
      ]
    }
  }
}
```

### 02-do requirements

Do **must**:
- execute only `plan.work_plan.do_steps[*]`
- record what was executed (actual work)

Do `input.json` must include:
- `plan.work_plan`
- `plan.acceptance_criteria.effective`

Do `output.json` must include:

```json
{
  "do": {
    "execution": {
      "executed_step_ids": ["DO-1"],
      "skipped_step_ids": [],
      "commands": [
        { "id": "CMD-1", "cmd": "go test ./...", "exit_code": 0 }
      ]
    },
    "blockers": [
      {
        "kind": "dependency|env|unknown",
        "text": "what blocked or surprised us",
        "suggested_stop_reason": "dependency_blocked|replan_required"
      }
    ]
  }
}
```

### 03-check requirements

Check **must**:
1) verify **plan match** (planned vs executed)
2) verify **job done** (all effective ACs evaluated)
3) emit a verdict used by Act

Check `input.json` must include:
- `plan.work_plan`
- `plan.acceptance_criteria.effective`
- `do.execution`

Check `output.json` must include:

```json
{
  "check": {
    "plan_match": {
      "do_steps": {
        "planned_ids": ["DO-1"],
        "executed_ids": ["DO-1"],
        "missing_ids": [],
        "unexpected_ids": []
      },
      "commands": {
        "planned_ids": ["CMD-1"],
        "executed_ids": ["CMD-1"],
        "missing_ids": [],
        "unexpected_ids": []
      }
    },

    "acceptance_results": [
      {
        "ac_id": "AC-1",
        "result": "PASS|FAIL",
        "notes": "...",
        "log_ref": "steps/03-check/logs/stdout.txt"
      },
      {
        "ac_id": "AC-1.1",
        "result": "PASS|FAIL",
        "notes": "...",
        "log_ref": "steps/03-check/logs/stdout.txt"
      }
    ],

    "verdict": {
      "status": "PASS|FAIL|PARTIAL",
      "recommendation": "standardize|replan|rollback|continue",
      "basis": {
        "plan_match": "MATCH|MISMATCH",
        "all_acceptance_passed": true
      }
    },

    "process_notes": [
      {
        "kind": "plan_mismatch|missing_verification",
        "severity": "warning|error",
        "text": "...",
        "suggested_stop_reason": "replan_required|none"
      }
    ]
  }
}
```

#### Verdict rules (enforceable)

- If any `acceptance_results[*].result == "FAIL"` → `verdict.status = "FAIL"`.
- Else if any `plan_match.*.missing_ids` or `plan_match.*.unexpected_ids` is non-empty → `verdict.status = "PARTIAL"`.
- Else → `verdict.status = "PASS"`.

### 04-act requirements

Act **must**:
- consume Check verdict
- decide what to do next

Act `input.json` must include:
- `check.verdict` (and optionally `check.acceptance_results`)

Act `output.json` must include:

```json
{
  "act": {
    "decision": "close|replan|rollback|continue",
    "rationale": "...",
    "next": {
      "recommended": true,
      "notes": "what must change in the next Plan"
    }
  }
}
```

## progress.md (Ralph-style run journal)

The orchestrator must append one entry to `artifacts/progress.md` after **every** step, derived from the step `output.json.progress` plus status fields.

### Append template

```md
## <UTC timestamp> — <step_index> <STEP_NAME> — <status>/<stop_reason>
**Task:** <task.id>  
**Run:** <run.id> · **Iteration:** <run.iteration>

**Title:** <progress.title>

**Details:**
- <progress.details[0]>
- <progress.details[1]>

**Logs:**
- stdout: <progress.links.stdout>
- stderr: <progress.links.stderr>
```

### Step progress expectations (minimum)

- **Plan:** include goal + counts (`AC effective`, `do_steps`, `check_steps`).
- **Do:** include executed vs skipped steps + command exit summary.
- **Check:** include plan_match summary + acceptance pass/fail counts + verdict.
- **Act:** include decision + what changes are required next.
