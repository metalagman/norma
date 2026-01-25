# norma — agents.md (MVP Spec, SQLite no-CGO)

This document defines the **MVP agent interface** for `norma` (written in Go) and the **MVP storage model** using **SQLite without CGO** (pure-Go driver), while keeping **artifacts on disk** and **run/step state in DB**. **Task state and backlog are Beads-only** and must not be mirrored in Norma state.

Single fixed workflow:
> `plan → do → check → act` (loop until PASS or budgets exhausted)

---

## 0) Design principles

- **One workflow**; flexibility comes from swapping agents per role.
- **Artifacts are files** (human-debuggable).
- **Run/step state is in SQLite** (queryable, UI-friendly).
- **Task state lives only in Beads** (no task/backlog state in Norma DB or kv).
- **Workspaces (Git Worktrees):** Every run MUST operate in a dedicated Git worktree located inside the run directory. Agents perform all work within this isolated workspace.
- **Shared Artifacts:** Every run has a shared `artifacts/` directory where agents produce evidence and consume data from previous steps. This replaces step-local artifact storage.
- **Task-scoped Branches:** Workspaces use Git branches scoped to the task: `norma/task/<task_id>`. This allows progress to be restartable across multiple runs.
- **Workflow State in Labels:** Granular workflow states (`planning`, `doing`, `checking`, `acting`) are tracked using `bd` labels on the task.
- **Git History as Source of Truth:** Agents communicate changes by modifying the workspace directly. The orchestrator extracts changes using Git history (e.g., `git diff HEAD`).
- **Any agent** is supported through a **normalized JSON contract**.

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
  locks/run.lock           # exclusive lock for "norma run"
  runs/<run_id>/
    norma.md               # goal + AC + budgets (human readable)
    workspace/             # Git worktree (the active workspace for this run)
    artifacts/             # Shared artifacts (produced and consumed by agents)
      plan.md
      verdict.json
      scorecard.md
      evidence/...
    steps/
      001-plan/
        input.json
        output.json
        logs/stdout.txt
        logs/stderr.txt
      002-do/
        input.json
        output.json
        logs/stdout.txt
        logs/stderr.txt
      003-check/
        input.json
        output.json
        logs/...
      004-act/
        input.json
        output.json
        logs/...
```

### Invariants
- **Backlog is the truth (Beads):** The backlog is managed by the `beads` tool. `norma` interacts with it via the `bd` executable.
- **Run state (SQLite):** SQLite is the authoritative source for:
  - run list / status
  - current iteration/cursor
  - step records
  - timeline events
- **Workspaces:** The `workspace/` directory is a Git worktree. Agents perform all work within this isolated workspace. The orchestrator tracks changes by inspecting the Git history/diff of the workspace.
- **No task state in Norma DB:** task status, priority, dependencies, and selection are managed in Beads only.
- **Artifacts:** The `artifacts/` directory contains all artifacts produced during the run. Agents MUST write their artifacts here and MAY read existing artifacts from here.
- Agents MUST only write inside their current `step_dir` (for logs/metadata) and the shared `artifacts/` directory, except for **Do** and **Act** which modify files in the `workspace/` to implement the selected issue or fix.

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

## 5) Fixed workflow

### Roles (fixed)
- `plan`  : outline approach and intent
- `do`    : generate evidence or execute work
- `check` : evaluate evidence against acceptance criteria, produce verdict
- `act`   : propose and implement fix

### Sane Norma Loop (PDCA)
State machine per iteration:

**PLAN**
- Inputs: current goal, backlog, latest observations (from last Check).
- Outputs: updated ordered backlog, Next Slice (1 feature + 1–3 tasks), stop conditions, verification checklist.

**DO**
- Execute only the Next Slice and produce concrete artifacts.
- If scope blows up or uncertainty appears: create a Spike or split tasks; do not expand scope inside Do.

**CHECK**
- Run verification for the slice (tests/lint/build, AC checks, diff-based artifact review).
- Classify: ✅ pass / ⚠️ partial / ❌ fail or wrong direction.

**ACT**
- Update backlog (done/added/reprioritized), record decisions (light ADR if needed).
- Decide next action: continue, re-plan, rollback, or ship.

Invariants:
- **Bounded work:** one loop executes max 1–3 tasks or one feature slice.
- **Always verifiable:** every task includes a Verify step that Check can run.
- **Backlog is the truth:** new work becomes tasks, not implicit context.

Stop conditions (trigger immediate re-plan during Do/Check):
- task size grows from S/M to L
- missing requirement discovered
- dependency blocks progress
- tests reveal systemic (non-local) issues
- more than N retries on the same task (default N=2)

Minimal backlog item format (Norma-friendly):
- Objective
- Artifact
- Verify
- Optional: Deps, Size (S/M/L; L forbidden—must be split)

Example:
- [ ] T12 (S) Add /v1/devices GET filtering | Artifact: api/devices.go + openapi.yaml | Verify: unit + curl happy-path

### Loop (MVP)
- Start run with `iteration=1`
- `plan` → `do` → `check`
- If `verdict=PASS`: mark run `passed`, stop
- Else `act`, `iteration++`, repeat

### Budgets
`norma` MUST stop when any budget is exceeded, with `status=stopped` and an event.

MVP budgets:
- `max_iterations` (required)
- optional: `max_patch_kb`, `max_changed_files`, `max_risky_files`

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
Output: one of three results

**A. READY**
- The selected task becomes executable (Ready Contract complete).
- Return: `next_task_id = selected_task_id`.

**B. DECOMPOSE**
- If selected task is epic/feature or “too big”, create child issues (parent-child).
- Ensure at least 1–3 children are Ready.
- Return: `next_task_id = <one Ready child leaf task>`.

**C. BLOCKED**
- If selected task is missing a prerequisite, create prerequisite issue and add `blocks`.
- Return: `next_task_id = <prerequisite issue>` (must be Ready or made Ready).

Plan agent allowed mutations:
- Create/update issues in the selected subtree (selected task + descendants)
- Add/remove **blocks** edges involving the selected subtree
- Create new issues marked as discovered work under the same parent feature
Plan agent forbidden:
- Reprioritizing unrelated features/epics
- Editing unrelated issues

Stop condition inside Plan:
- If no Ready leaf can be produced, return BLOCKED with explicit prerequisite.

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

### 3) CHECK (Tool or Check Agent)
Input:
- `Verify` field from the task
Output:
- PASS / FAIL / PARTIAL
- Evidence (test output summary, commands run, links to artifacts)

### 4) ACT (Orchestrator)
If PASS:
- Close `next_task_id`
- Extract changes from `workspace/` and apply to main repository
- Append a short entry to `progress.md` (what worked, what to repeat)

If FAIL or PARTIAL:
- Keep task open (or reopen)
- Optionally create “Fix …” child task(s) and/or spike(s)
- Update deps if a prerequisite was discovered
- Append learnings to `progress.md`

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

### 6.2 Agent configuration (MVP)
Stored in `.norma/config.json` (or repo `.norma.json` — your choice later).

Example:
```json
{
  "agents": {
    "plan":  { "type": "exec",  "cmd": ["./bin/norma-agent-plan"] },
    "do":    { "type": "exec",  "cmd": ["./bin/norma-agent-do"] },
    "check": { "type": "exec",  "cmd": ["./bin/norma-agent-check"] },
    "act":   { "type": "codex", "model": "gpt-5-codex" }
  },
  "budgets": {
    "max_iterations": 5,
    "max_patch_kb": 200
  },
  "retention": {
    "keep_last": 50,
    "keep_days": 30
  }
}
```

Notes:
- `cmd` is an argv array for safety.
- For `codex`, `opencode`, and `gemini`, `cmd` is not supported; the tool binary is fixed.
- `codex.path`, `opencode.path`, and `gemini.path` should constrain context (repo root or subdir).
- `retention.keep_last` and `retention.keep_days` control auto-pruning on each run (optional).

---

## 7) Agent contract (JSON)

### 7.1 Request: AgentRequest (written to `steps/<n>/input.json` and used as invocation input)
```json
{
  "version": 1,
  "run_id": "20260123-145501-ab12cd",
  "step": {
    "index": 2,
    "role": "check",
    "iteration": 1
  },
  "goal": "Short human goal",
  "norma": {
    "acceptance_criteria": [
      { "id": "AC1", "text": "All unit tests pass" },
      { "id": "AC2", "text": "No lint errors" }
    ],
    "budgets": {
      "max_iterations": 5,
      "max_patch_kb": 200
    }
  },
  "paths": {
    "repo_root": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd/workspace",
    "run_dir": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd",
    "step_dir": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd/steps/003-check",
    "artifacts_dir": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd/artifacts"
  },
  "context": {
    "artifacts": [
      "plan.md"
    ],
    "next_actions": [
      "Implement feature X"
    ],
    "notes": "Optional free-form"
  },
  "plan": {
    "task": { "id": "bd-1" }
  },
  "do": {
    "task": { "id": "bd-1" }
  }
}
```

**Rules**
- Agent MUST treat `step_dir` as the location for step-specific logs and metadata.
- Agent MUST treat `artifacts_dir` as the primary location for producing evidence, plans, and other shared artifacts.
- Agent MAY read existing artifacts from `artifacts_dir`.
- Agent MUST NOT write outside `step_dir` or `artifacts_dir`, EXCEPT for `do` and `act` which modify the `workspace/`.
- `paths.repo_root` points to the isolated Git worktree (`workspace/`).
- `plan` and `do` blocks are role-specific and only present for their respective roles.
- `context.next_actions` contains the `next_actions` suggested by the previous step in the same iteration.

### 7.2 Response: AgentResponse (norma expects JSON on stdout; stored as `steps/<n>/output.json`)
```json
{
  "version": 1,
  "status": "ok",
  "summary": "What happened, 1-3 sentences",
  "files": [
    "evidence/test.log",
    "scorecard.md"
  ],
  "next_actions": [
    "Re-run tests",
    "Re-check AC2 using golangci-lint"
  ],
  "errors": []
}
```

**Rules**
- `status` is `ok` or `fail`.
- `files` MUST be relative paths under `artifacts_dir` (no `..`, no absolute paths).
- If stdout is not valid JSON:
  - norma MUST store raw stdout in logs and mark step failed (`protocol_error`).

---

## 8) Role-specific requirements

### 8.1 Role: plan
Purpose: outline approach and intent.

MUST:
- Produce `output.json` (AgentResponse)
- Write logs:
  - `logs/stdout.txt`, `logs/stderr.txt` (norma captures these)
SHOULD:
- Write `plan.md` in `artifacts_dir` with ordered backlog, Next Slice, stop conditions, and verification checklist (validated by norma)

### 8.2 Role: do
Purpose: implement work in workspace.

MUST:
- Produce `output.json` (AgentResponse)
- Write logs:
  - `logs/stdout.txt`, `logs/stderr.txt` (norma captures these)
- Implement the selected task by modifying files in `workspace/`.
SHOULD:
- Write evidence under `artifacts_dir` (e.g. `evidence/run.log`, `evidence/commands.txt`)

### 8.3 Role: check
Purpose: evaluate workspace vs acceptance criteria.

MUST:
- Produce `verdict.json` in `artifacts_dir`
- Produce `scorecard.md` in `artifacts_dir` (human-readable summary)
- Produce `output.json`

`verdict.json` schema:
```json
{
  "version": 1,
  "verdict": "PASS",
  "criteria": [
    {
      "id": "AC1",
      "text": "All unit tests pass",
      "pass": true,
      "evidence": "evidence/test.log"
    }
  ],
  "metrics": {
    "tests_passed": 123,
    "tests_failed": 0
  },
  "blockers": [],
  "recommended_fix": []
}
```

Rules:
- `verdict` MUST be `PASS` or `FAIL`.
- If `verdict.json` missing or invalid → norma stops run as failed (`protocol_error`).

### 8.4 Role: act
Purpose: propose and implement fix in workspace.

MUST:
- Produce `output.json`
SHOULD:
- Modify files in the `workspace/` to implement the proposed fix.
- Explain in `summary` which ACs it targets and why

Rules:
- Agent does not apply changes to the main repository.
- The `workspace/` serves as the staging area.

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
- write required files for the role (check: verdict.json + scorecard.md)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir` or `artifacts_dir` (or `workspace/` for `do`/`act`)
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

## 13) OpenCode oneshot backend (MVP)

OpenCode typically outputs free-form text. MVP requires deterministic output:

### OpenCode prompt policy (MUST)
norma generates a role-specific prompt that instructs OpenCode to:
- write required files for the role (check: verdict.json + scorecard.md)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir` or `artifacts_dir` (or `workspace/` for `do`/`act`)
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

---

## 14) Gemini oneshot backend (MVP)

Gemini CLI typically outputs free-form text. MVP requires deterministic output:

### Gemini prompt policy (MUST)
norma generates a role-specific prompt that instructs Gemini to:
- write required files for the role (check: verdict.json + scorecard.md)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir` or `artifacts_dir` (or `workspace/` for `do`/`act`)
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

---

## 15) Acceptance checklist (MVP)

- [ ] `norma run <task-id>` creates a run and DB entry in `.norma/norma.db`
- [ ] Each run creates an isolated Git worktree at `runs/<run_id>/workspace/`
- [ ] Each run uses a task-scoped Git branch: `norma/task/<task_id>`
- [ ] Workflow states are tracked via `bd` labels on the task
- [ ] Each run has a shared `artifacts/` directory for shared data
- [ ] Each step creates artifacts in `runs/<run_id>/steps/<n>-<role>/`
- [ ] check produces `verdict.json` + `scorecard.md`
- [ ] Successful runs extract changes from `workspace/` and apply them to the main repo
- [ ] Crash recovery cleans tmp dirs and reconciles missing DB step records

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
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