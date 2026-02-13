# norma — agents.md (MVP Spec, SQLite no-CGO)

This document defines the **MVP agent interface** for `norma` (written in Go) and the **MVP storage model** using **SQLite without CGO** (pure-Go driver), while keeping **artifacts on disk** and **run/step state in DB**. **Task state and backlog are Beads-only** and must not be mirrored in Norma state.

Single fixed workflow:
> `plan → do → check → act` (loop until PASS or budgets exhausted)

---

## 0) Design principles

- **One workflow**; flexibility comes from swapping agents per role.
- **Artifacts are files** (human-debuggable).
- **Run/step state is in SQLite** (queryable, UI-friendly).
- **Task state lives in Beads** (source of truth for progress and resumption).
- **Task Notes as State Object:** The Beads `notes` field stores a comprehensive JSON object (`TaskState`) containing step outputs and a full run journal. This allows full state recovery and resumption across different environments.
- **Workspaces (Git Worktrees):** Every role agent step run MUST operate in a dedicated Git worktree located inside its step directory (`<step_dir>/workspace`). Agents perform all work within this isolated workspace.
- **Run Journal:** Every agent step has an `artifacts/progress.md` file. It is reconstructed from the task's `Journal` state by the orchestrator before the agent is invoked.
- **Task-scoped Branches:** Workspaces use Git branches scoped to the task: `norma/task/<task_id>`. This allows progress to be restartable across multiple runs.
- **Workflow State in Labels:** Granular states (`norma-has-plan`, `norma-has-do`, `norma-has-check`) are used to track completed steps and skip them during resumption.
- **Git History as Source of Truth:** The orchestrator extracts changes from the workspace using Git (e.g., `git merge --squash`).
- **Any agent** is supported through a **normalized JSON contract**.

## 0.1) Agent guidelines (for Norma development)

When working ON the `norma` project itself, agents MUST:
- **Follow Google Go Best Practices**: Adhere strictly to the principles and idioms defined in the Google Go Style Guide and Best Practices.
- **Use project-local tools**: Always prefer project-local tools via `go tool` (e.g., `go tool golangci-lint run`).
- **Always verify changes**: MUST run tests using `go test ./...` and linters (`go tool golangci-lint run`) before submitting code changes. Never assume code is correct without passing local quality gates.

## 0.2) Contributing requirements

All contributors MUST:
- **Start with a tracked issue**: File or reference a Beads issue before opening a PR.
- **Follow Conventional Commits**: Use Conventional Commits for all new commits.
- **Pass quality gates**: `go test ./...` and `go tool golangci-lint run` must pass before submission.
- **Sync via merge (no rebase)**: Use merge-based pulls when updating from origin (`git pull --no-rebase` or `git pull --merge`). Do not rebase shared branches.

---

## 1) Directory layout

Everything lives under the project root:

```
.beads/                    # Beads backlog storage (source of truth for tasks)
  issues.jsonl             # Issues, Epics, and Features
  metadata.json
  interactions.jsonl
.norma/
  norma.db                 # SQLite DB (source of truth for run/step state)
  locks/run.lock           # exclusive lock for "norma loop"
      runs/<run_id>/
      norma.md               # goal + AC + budgets (human readable)
      steps/
        01-plan/
          input.json
          output.json
          workspace/         # Git worktree for this specific step
          artifacts/
            progress.md      # Run journal (always reconstructed)
          logs/
            stdout.txt
            stderr.txt
        02-do/
          input.json
          output.json
          workspace/         # Git worktree for this specific step
          artifacts/
            progress.md
          logs/
            stdout.txt
            stderr.txt
  
```

### Invariants
- **Backlog is the truth (Beads):** The backlog is managed by the `beads` tool. `norma` interacts with it via the `bd` executable.
- **Run state (SQLite):** SQLite is the authoritative source for:
  - run list / status
  - current iteration/cursor
  - step records
  - timeline events
- **Workspaces:** Every role agent step run gets its own Git worktree in the `<step_dir>/workspace`. Agents perform all work within this isolated workspace. The orchestrator tracks changes by inspecting the Git history/diff of the workspace (primarily in Do and Act).
- **No task state in Norma DB:** task status, priority, dependencies, and selection are managed in Beads only.
- **Artifacts:** The `artifacts/` directory contains all artifacts produced during the run. Agents MUST write their artifacts here and MAY read existing artifacts from here.
- Agents MUST only write inside their current `step_dir` (for logs/metadata, and the `workspace/` subdir) and the shared `artifacts/` directory.

---

## 2) SQLite storage (no CGO)

### DB file
- `.norma/norma.db`

### Connection policy (MVP)
- Use a single writer connection (to avoid multi-writer pool contention):
  - `db.SetMaxOpenConns(1)`
  - `db.SetMaxIdleConns(1)`

### Required PRAGMAs (MVP)
Run once on open:
- `PRAGMA foreign_keys=ON;`
- `PRAGMA journal_mode=WAL;` (if it fails, proceed in default mode but log it)
- `PRAGMA busy_timeout=5000;`

---

## 3) Schema (MVP)

### 3.1 schema_migrations
Tracks schema versions (simple integer migration).

Columns:
- `version INTEGER PRIMARY KEY`
- `applied_at TEXT NOT NULL`

### 3.2 runs
Columns:
- `run_id TEXT PRIMARY KEY`
- `created_at TEXT NOT NULL`          (RFC3339)
- `goal TEXT NOT NULL`
- `status TEXT NOT NULL`              (`running|passed|failed|stopped`)
- `iteration INTEGER NOT NULL DEFAULT 0`
- `current_step_index INTEGER NOT NULL DEFAULT 0`
- `verdict TEXT NULL`                 (`PASS|FAIL`)
- `run_dir TEXT NOT NULL`             (absolute or repo-relative)

### 3.3 steps
Primary key: `(run_id, step_index)`

Columns:
- `run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE`
- `step_index INTEGER NOT NULL`
- `role TEXT NOT NULL`                (`plan|do|check|act`)
- `iteration INTEGER NOT NULL`
- `status TEXT NOT NULL`              (`ok|fail|skipped`)
- `step_dir TEXT NOT NULL`
- `started_at TEXT NOT NULL`          (RFC3339)
- `ended_at TEXT NULL`                (RFC3339)
- `summary TEXT NULL`

### 3.4 events (timeline)
Primary key: `(run_id, seq)`

Columns:
- `run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE`
- `seq INTEGER NOT NULL`              (monotonic per run)
- `ts TEXT NOT NULL`                  (RFC3339)
- `type TEXT NOT NULL`                (e.g. `run_started`, `step_committed`, `verdict`)
- `message TEXT NOT NULL`
- `data_json TEXT NULL`               (optional structured payload)

### 3.5 kv_run (optional)
Primary key: `(run_id, key)`

Columns:
- `run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE`
- `key TEXT NOT NULL`
- `value_json TEXT NOT NULL`
Notes:
- `kv_run` is for run-scoped metadata only (e.g., active feature id for a run UI), not for task/backlog state.

---

## 4) Atomicity & crash recovery

### 4.1 Step commit protocol (MUST)
A step is committed in this order:

1) Create step dir: `steps/003-check/`
2) Write all step files inside it (inputs, outputs, logs, verdict, etc).
3) DB transaction (`BEGIN IMMEDIATE` recommended):
   - insert step record into `steps`
   - append one or more records into `events`
   - update `runs.current_step_index`, `runs.iteration`, `runs.verdict/status` if applicable
4) `COMMIT`

If the process crashes:
- Artifacts might exist without a matching DB record.

### 4.2 Reconciliation on startup (MVP MUST)
On `norma` start:
- For each run in `.norma/runs/*`:
  - list `steps/<NNN-role>/`
  - ensure there is a matching DB `steps` record
  - if missing, insert a minimal record with `status=fail` and an event like:
    - type `reconciled_step`, message `Step dir exists but DB record was missing; inserted during recovery`
  - do not attempt to “guess” verdict; only store references.

---

## 5) Fixed workflow (norma-loop)

Run the **single fixed** Norma workflow: **Plan → Do → Check → Act** until **PASS** or a **stop condition** triggers.

### Core invariants

1. **One workflow only:** `plan -> do -> check -> act` (repeat).
2. **Workspace exists before any agent runs:** the orchestrator creates `<step_dir>/workspace/` before each step. This workspace is a dedicated Git worktree.
3. **Agents never modify workspace or git directly except for file writes in Do:** all agents operate in **read-only** mode with respect to `workspace/` by default. Using `git` commands from within agents is **STRICTLY FORBIDDEN**.
4. **Orchestrator commits changes in Do:** If the Do agent finishes with status `ok`, the orchestrator is responsible for staging and committing all changes in the `workspace/` Git repository.
5. **Commits/changes in main repo happen outside agents:** the orchestrator extracts changes from the task branch and applies them to the main repository.
6. **Contracts are JSON only:** every step is `input.json → output.json`.
7. **Every step MUST produce output.json:** The agent MUST write its AgentResponse JSON to `output.json` in the step directory.
8. **Every step captures logs:**
    - `steps/<n>-<role>/logs/stdout.txt`
    - `steps/<n>-<role>/logs/stderr.txt`
   - Agent `stdout`/`stderr` MUST be mirrored to terminal only when debug mode is enabled.
9. **Run journal:** the orchestrator appends one entry after every step to:
    - `artifacts/progress.md`
10. **Acceptance criteria (AC):** baseline ACs are passed into Plan; Plan may extend them with traceability.
11. **Check compares plan vs actual and verifies job done:** Check must compare the Plan work plan to Do execution and evaluate all effective ACs.
12. **Verdict goes to Act:** Act receives Check verdict and decides next.
13. **Agents are invoked with `<step_dir>` as their current working directory.**

### Budgets and stop conditions

The orchestrator must stop immediately when any applies:
- budget exceeded (iterations / wall time / failed checks / retries)
- dependency blocked
- verify missing (verification cannot run as planned)
- replan required (Plan cannot produce a safe/complete work plan)

Stopping must be reflected in `output.json` with `status="stop"` and a concrete `stop_reason`.

### Task IDs

- `task.id` must match: `^norma-[a-z0-9]+(?:\.[a-z0-9]+)*$`
    - examples: `norma-a3f2dd`, `norma-01`, `norma-fixlogin2`, `norma-4pm.1.1`
- Non-matching IDs → Plan must stop with `stop_reason="replan_required"` (reason in logs).

---

# Norma PDCA Loop (bd-backed)

Norma runs a tight PDCA cycle over the `bd` graph. `bd` is the single source of truth for backlog, hierarchy (parent-child), and hard prerequisites (blocks). Norma orchestrator selects work; agents refine and execute.

## Concepts

### Issue types
- **epic**: big outcome + acceptance criteria (AC)
- **feature**: slice of value under an epic (should be verifiable)
- **task/bug**: executable unit
- **spike**: resolve an unknown → information artifact

### Relationships
- **parent-child**: hierarchy (epic → feature → task/spike/bug)
- **blocks**: hard prerequisite (B blocks A = B must complete before A)
- **related**: soft link (optional)
- **discovered-from**: used when new work is discovered during Do (optional)

### “Ready” (execution gate)
An issue is **Ready** if:
- `bd ready` includes it (status open AND no blocking deps)
- and it is a **leaf** (no children), unless explicitly selected for decomposition
- and its description contains the Ready Contract fields (below)

### Ready Contract (must be present in description for executable tasks)
- **Objective**: what this issue accomplishes
- **Artifact**: where the change lands (files/paths/PR)
- **Verify**: commands/checks that prove it works

(Spikes can use Verify = “unknown resolved + notes captured”.)

**Workflow State in Labels:** Granular workflow states (`planning`, `doing`, `checking`, `acting`) are tracked using `bd` labels on the task.
- `norma-has-plan`: Present if a valid work plan exists in task notes. Skips Plan step.
- `norma-has-do`: Present if work has been implemented in the workspace. Skips Do step.
- `norma-has-check`: Present if a verdict has been produced. Skips Check step.

---

## PDCA Responsibilities (who does what)

### Orchestrator responsibilities
- Select the next issue deterministically (scheduler)
- Enforce WIP limits
- Enforce focus (active epic/feature)
- Run the PDCA loop steps and record outcomes

### Agent responsibilities
- Plan agent: refine/decompose a *selected* issue into Ready leaf tasks
- Do agent: implement exactly one Ready leaf task
- Check agent/tool: run Verify steps and attach evidence
- Agents must not perform global reprioritization outside the selected subtree

---

## Orchestrator: Selection Policy

Input: `bd ready` list

Default selection algorithm:
1. Prefer issues under `active_feature_id` (if set), else under `active_epic_id`.
2. Prefer **leaf** issues (no children).
3. Sort by `priority` ascending (0 highest).
4. Tie-breakers:
   - Has Verify field (quality) first
   - Oldest open first (FIFO)

Output:
- `selected_task_id`
- `selection_reason` (short string)

WIP:
- At most 1 task per agent (or 2 if one is a spike).

---

## PDCA Loop (single iteration)

### 1) PLAN (Plan Agent, scoped)
Input:
- `bd show <selected_task_id>`
- parent chain (optional): epic/feature context
- current `progress.md` (optional)
Output: one of two results

**A. READY**
- The selected task becomes executable (Ready Contract complete).
- Return: `next_task_id = selected_task_id`.

**B. BLOCKED**
- If selected task is missing a prerequisite, create prerequisite issue and add `blocks`.
- Return: `next_task_id = <prerequisite issue>` (must be Ready or made Ready).

Stop condition inside Plan:
- If no Ready task can be produced, return BLOCKED with explicit prerequisite.

### 2) DO (Do Agent)
Input:
- `bd show <next_task_id>` (must be Ready) + repo + conventions
Output:
- code/doc artifacts in `workspace/`
- proposed status change
- anything discovered → new issues under same parent

Do agent rules:
- Work on exactly one task per iteration (`next_task_id`)
- Do not start additional ready issues
- If scope grows, split: create new child tasks and stop
- Do NOT perform any `git` commands (add, commit, etc.). The orchestrator will handle committing your file changes.

### 3) CHECK (Tool or Check Agent)
Input:
- `Verify` field from the task
Output:
- PASS / FAIL / PARTIAL
- Evidence (test output summary, commands run, links to artifacts)

### 4) ACT (Orchestrator)
The orchestrator persists the entire `TaskState` (Plan, Do, Check outputs + Journal) to the Beads `notes` field after every step.

If PASS:
- Close `next_task_id`.
- Extract changes from `workspace/` and apply to main repository using `git merge --squash`.
- Create a Conventional Commit.

If FAIL or PARTIAL:
- Keep task open (or reopen).
- The PDCA loop continues to the next iteration or stops based on the `act` agent decision.

Then loop.

---

## Completion Rules

### Feature complete
A feature is complete when:
- All descendant leaf issues are closed
- Feature-level acceptance checklist (in feature description) is satisfied

### Epic complete
An epic is complete when:
- All features under it are complete
- Epic-level acceptance criteria are satisfied

---

## Suggested description templates

### Feature description
- Goal:
- Acceptance:
  - [ ] ...
  - [ ] ...
- Notes/Constraints:

### Task description (Ready Contract)
- Objective:
- Artifact:
- Verify:
- Notes (optional):

### Spike description
- Objective (unknown to resolve):
- Artifact (notes/doc/decision):
- Verify (how we know unknown is resolved):

---

## Notes on dependency hygiene
- Use `blocks` only for true prerequisites (avoid over-blocking).
- Prefer parent-child for structure, and `related` for soft links.
- Regularly run `bd dep cycles` to prevent deadlocks.

---

## 6) Agent system

### 6.1 Agent types (MVP)
- `exec`  : spawn local binary, JSON on stdin/stdout
- `codex` : run Codex CLI oneshot using a generated prompt (norma enforces JSON output)
- `opencode` : run OpenCode CLI oneshot using a generated prompt (norma enforces JSON output)
- `gemini` : run Gemini CLI oneshot using a generated prompt (norma enforces JSON output)
- `claude` : run Claude CLI oneshot using a generated prompt (norma enforces JSON output)

### 6.2 Agent configuration (MVP)
Stored in `.norma/config.yaml`.

Example:
```yaml
profile: default

agents:
  opencode_exec_agent:
    type: exec
    cmd: ["./bin/norma-agent-opencode-exec"]
  codex_agent:
    type: codex
    model: gpt-5-codex

profiles:
  default:
    pdca:
      plan: opencode_exec_agent
      do: opencode_exec_agent
      check: codex_agent
      act: codex_agent
    features:
      backlog_refiner:
        agents:
          planner: codex_agent
          implementer: opencode_exec_agent

budgets:
  max_iterations: 5
  max_patch_kb: 200

retention:
  keep_last: 50
  keep_days: 30
```

Notes:
- `cmd` is an argv array for safety.
- For `codex`, `opencode`, `gemini`, and `claude`, `cmd` is not supported; the tool binary is fixed.
- `codex.path`, `opencode.path`, `gemini.path`, and `claude.path` should constrain context (repo root or subdir).
- `profiles.<name>.features.*.agents.*` must reference keys defined in top-level `agents`.
- `retention.keep_last` and `retention.keep_days` control auto-pruning on each run (optional).

---

## 7) Agent contracts (JSON)

Every step is an `input.json → output.json` transformation. The agent MUST produce an `output.json` file in the assigned step directory containing the valid AgentResponse JSON.

Contracts are formally defined by JSON schemas located in `internal/workflows/normaloop/<role>/*.schema.json`.

### 7.1 Common input.json (all steps)

All agent inputs share a common structure, extended with role-specific fields.

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
    "name": "plan|do|check|act"
  },
  "paths": {
    "workspace_dir": "workspace",
    "run_dir": "./",
    "progress": "artifacts/progress.md"
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
    "links": [],
    "attempt": 0
  }
}
```

### 7.2 Common output.json (all steps)

All agent outputs share a common structure, extended with role-specific fields.

```json
{
  "status": "ok|stop|error",
  "stop_reason": "none|budget_exceeded|dependency_blocked|verify_missing|replan_required",
  "summary": {
    "text": "short human summary",
    "warnings": [],
    "errors": []
  },
  "progress": {
    "title": "short line for the run journal",
    "details": [
      "bullet 1",
      "bullet 2"
    ]
  },
  "logs": {
    "stdout_path": "steps/<n>-<role>/logs/stdout.txt",
    "stderr_path": "steps/<n>-<role>/logs/stderr.txt"
  },
  "timing": {
    "wall_time_ms": 0
  }
}
```

---

## 8) Role-specific requirements (Step Requirements)

### 8.1 Role: 01-plan

Plan **must**:
- produce `work_plan` (the iteration plan)
- publish `acceptance_criteria.effective` (may extend baseline with traceability)

Plan `output.json` must include:

```json
{
  "plan_output": {
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
          "targets_ac_ids": ["AC-1"]
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

### 8.2 Role: 02-do

Do **must**:
- execute only `plan_output.work_plan.do_steps[*]`
- record what was executed (actual work)

Do `input.json` must include:
- `do_input.work_plan`
- `do_input.acceptance_criteria_effective`

Do `output.json` must include:

```json
{
  "do_output": {
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

### 8.3 Role: 03-check

Check **must**:
1) verify **plan match** (planned vs executed)
2) verify **job done** (all effective ACs evaluated)
3) emit a verdict used by Act

Check `input.json` must include:
- `check_input.work_plan`
- `check_input.acceptance_criteria_effective`
- `check_input.do_execution`

Check `output.json` must include:

```json
{
  "check_output": {
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

### 8.4 Role: 04-act

Act **must**:
- consume Check verdict
- decide what to do next

Act `input.json` must include:
- `act_input.check_verdict` (and optionally `act_input.acceptance_results`)

Act `output.json` must include:

```json
{
  "act_output": {
    "decision": "close|replan|rollback|continue",
    "rationale": "...",
    "next": {
      "recommended": true,
      "notes": "what must change in the next Plan"
    }
  }
}
```

---

## 8.5 progress.md (Ralph-style run journal)

The orchestrator maintains a `Journal` in the task's `TaskState`. Before every step, the `artifacts/progress.md` file is reconstructed in the step directory from the existing `Journal`.

### Append template

```md
## <UTC timestamp> — <step_index> <STEP_NAME> — <status>/<stop_reason>
**Task:** <task.id>  
**Run:** <run.id> · **Iteration:** <run.iteration>

**Title:** <progress.title>

**Details:**
- <progress.details[0]>
- <progress.details[1]>
```

### Step progress expectations (minimum)
- **Plan:** include goal + counts (`AC effective`, `do_steps`, `check_steps`).
- **Do:** include executed vs skipped steps + command exit summary.
- **Check:** include plan_match summary + acceptance pass/fail counts + verdict.
- **Act:** include decision + what changes are required next.

---

## 9) Applying Changes (norma responsibility)

norma extracts changes from the ephemeral workspace using Git:
- When a run reaches a `PASS` verdict, norma extracts changes from `workspace/` (e.g., via `git diff HEAD`).
- norma applies the captured changes to the main repository atomically:
  - record git status/hash "before"
  - apply changes
  - if successful, commit changes using **Conventional Commits** format:
    - `fix: <summary>` or `feat: <summary>` based on the goal/task
    - Include `run_id` and `step_index` in the commit footer
  - record git status/hash "after"
- On apply failure:
  - rollback to "before" (best-effort)
  - mark run failed and stop

---

## 10) Commit Conventions (MUST)

All git commits generated by `norma` MUST follow the **Conventional Commits** specification.

Format: `<type>[optional scope]: <description>`

Common types:
- `feat`: A new feature
- `fix`: A bug fix
- `docs`: Documentation only changes
- `style`: Changes that do not affect the meaning of the code (white-space, formatting, etc)
- `refactor`: A code change that neither fixes a bug nor adds a feature
- `perf`: A code change that improves performance
- `test`: Adding missing tests or correcting existing tests
- `chore`: Changes to the build process or auxiliary tools and libraries

---

## 11) Exec backend (MVP)

### Invocation
- Request JSON is passed on `stdin`.
- AgentResponse JSON must be written to `stdout`.
- norma captures `stdout` and `stderr` into:
  - `logs/stdout.txt`
  - `logs/stderr.txt`
- norma MUST NOT stream agent `stdout`/`stderr` to terminal unless debug mode is enabled.

### Errors
- Non-zero exit code:
  - mark step failed
  - store logs
  - stop run

---

## 12) Codex oneshot backend (MVP)

Codex typically outputs free-form text. MVP requires deterministic output:

### Codex prompt policy (MUST)
norma generates a role-specific prompt that instructs Codex to:
- output ONLY valid JSON for AgentResponse on stdout
- write only inside the current step directory (or `workspace/` for `do`/`act`)

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error
- terminal mirroring of agent stdout/stderr is debug-only

---

## 13) OpenCode oneshot backend (MVP)

OpenCode typically outputs free-form text. MVP requires deterministic output:

### OpenCode prompt policy (MUST)
norma generates a role-specific prompt that instructs OpenCode to:
- output ONLY valid JSON for AgentResponse on stdout
- write only inside the current step directory (or `workspace/` for `do`/`act`)

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error
- terminal mirroring of agent stdout/stderr is debug-only

---

## 14) Gemini oneshot backend (MVP)

Gemini CLI typically outputs free-form text. MVP requires deterministic output:

### Gemini prompt policy (MUST)
norma generates a role-specific prompt that instructs Gemini to:
- output ONLY valid JSON for AgentResponse on stdout
- write only inside the current step directory (or `workspace/` for `do`/`act`)

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error
- terminal mirroring of agent stdout/stderr is debug-only

---

## 15) Claude oneshot backend (MVP)

Claude CLI typically outputs free-form text. MVP requires deterministic output:

### Claude prompt policy (MUST)
norma generates a role-specific prompt that instructs Claude to:
- output ONLY valid JSON for AgentResponse on stdout
- write only inside the current step directory (or `workspace/` for `do`/`act`)

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error
- terminal mirroring of agent stdout/stderr is debug-only

---

## 16) Acceptance checklist (MVP)

- [x] `norma init` initializes .beads, .norma directory and default config.yaml
- [x] `norma loop <task-id>` creates a run and DB entry in `.norma/norma.db`
- [x] Each agent step run creates an isolated Git worktree at `<step_dir>/workspace/`
- [x] Each run uses a task-scoped Git branch: `norma/task/<task_id>`
- [x] Workflow states are tracked via `bd` labels on the task
- [x] Each agent step directory contains its own reconstructed `artifacts/progress.md`
- [x] Each step creates artifacts (`input.json`, `output.json`, `logs/`) in `runs/<run_id>/steps/<n>-<role>/`
- [x] Successful runs extract changes from the step workspace and apply them to the main repo
- [x] Crash recovery cleans tmp dirs and reconciles missing DB step records

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --no-rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
