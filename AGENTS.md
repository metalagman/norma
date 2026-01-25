# norma — agents.md (MVP Spec, SQLite no-CGO)

This document defines the **MVP agent interface** for `norma` (written in Go) and the **MVP storage model** using **SQLite without CGO** (pure-Go driver), while keeping **artifacts on disk** and **state/indexes in DB**.

Single fixed workflow:
> `plan → do → check → act` (loop until PASS or budgets exhausted)

---

## 0) Design principles

- **One workflow**; flexibility comes from swapping agents per role.
- **Artifacts are files** (human-debuggable).
- **Run/step state is in SQLite** (queryable, UI-friendly).
- **Atomic commits**: a step is committed by an atomic directory rename + a DB transaction.
- **Any agent** is supported through a **normalized JSON contract**.

---

## 1) Directory layout

Everything lives under the project root:

```
.norma/
  norma.db                 # SQLite DB (source of truth for run/step state)
  locks/run.lock           # exclusive lock for "norma run"
  runs/<run_id>/
    norma.md               # goal + AC + budgets (human readable)
    steps/
      001-plan/
        input.json
        output.json
        logs/stdout.txt
        logs/stderr.txt
        plan.md            # OPTIONAL
      002-do/
        input.json
        output.json
        logs/stdout.txt
        logs/stderr.txt
        files/...
      003-check/
        input.json
        output.json
        verdict.json        # REQUIRED
        scorecard.md        # REQUIRED
        logs/...
      004-act/
        input.json
        output.json
        patch.diff          # OPTIONAL (usually required when FAIL)
        logs/...
```

### Invariants
- Agents MUST only write inside their current `step_dir`.
- Step directories appear only when complete (created under a temp name then renamed).
- SQLite is the authoritative source for:
  - run list / status
  - current iteration/cursor
  - step records
  - timeline events
- Files in `runs/<run_id>/steps/...` are authoritative artifacts.

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

---

## 4) Atomicity & crash recovery

### 4.1 Step commit protocol (MUST)
A step is committed in this order:

1) Create temp step dir:
   - `steps/003-check.tmp-<rand>/`
2) Write all step files inside it (inputs, outputs, logs, verdict/patch, etc).
3) Rename temp dir to final dir:
   - `rename(tmpDir, steps/003-check/)`  (atomic on same filesystem)
4) DB transaction (`BEGIN IMMEDIATE` recommended):
   - insert step record into `steps`
   - append one or more records into `events`
   - update `runs.current_step_index`, `runs.iteration`, `runs.verdict/status` if applicable
5) `COMMIT`

If the process crashes:
- after (3) but before (5): artifacts exist, DB missing step → reconcile on startup.
- before (3): only temp dir exists → cleanup temp dir on startup.

### 4.2 Reconciliation on startup (MVP MUST)
On `norma` start:
- Delete any `steps/*.tmp-*` directories older than a short threshold (e.g. 5 minutes) OR unconditionally (MVP can be unconditional).
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
- `act`   : propose patch + next plan

### Loop (MVP)
- Start run with `iteration=1`
- `plan` → `do` → `check`
- If `verdict=PASS`: mark run `passed`, stop
- Else `act`, apply patch (by norma), `iteration++`, repeat

### Budgets
`norma` MUST stop when any budget is exceeded, with `status=stopped` and an event.

MVP budgets:
- `max_iterations` (required)
- optional: `max_patch_kb`, `max_changed_files`, `max_risky_files`

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
  }
}
```

Notes:
- `cmd` is an argv array for safety.
- For `codex`, `opencode`, and `gemini`, `cmd` is not supported; the tool binary is fixed.
- `codex.path`, `opencode.path`, and `gemini.path` should constrain context (repo root or subdir).

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
    "repo_root": "/abs/path/to/repo",
    "run_dir": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd",
    "step_dir": "/abs/path/to/repo/.norma/runs/20260123-145501-ab12cd/steps/003-check"
  },
  "context": {
    "previous_step_dirs": [
      "/abs/.../steps/001-plan"
    ],
    "notes": "Optional free-form"
  }
}
```

**Rules**
- Agent MUST treat `step_dir` as the only writable location.
- Agent MUST NOT write outside `step_dir`.
- If agent needs to reference files, it should reference repo files read-only unless role explicitly permits patch proposal (`act`).

### 7.2 Response: AgentResponse (norma expects JSON on stdout; stored as `steps/<n>/output.json`)
```json
{
  "version": 1,
  "status": "ok",
  "summary": "What happened, 1-3 sentences",
  "files": [
    "files/test.log",
    "scorecard.md"
  ],
  "next_actions": [
    "Apply patch.diff and re-run tests",
    "Re-check AC2 using golangci-lint"
  ],
  "errors": []
}
```

**Rules**
- `status` is `ok` or `fail`.
- `files` MUST be relative paths under `step_dir` (no `..`, no absolute paths).
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
- Write `plan.md` with an outline for the cycle

MUST NOT:
- Produce `patch.diff` (ignored if present)

### 8.2 Role: do
Purpose: generate evidence or execute work.

MUST:
- Produce `output.json` (AgentResponse)
- Write logs:
  - `logs/stdout.txt`, `logs/stderr.txt` (norma captures these)
SHOULD:
- Write evidence under `files/` (e.g. `files/run.log`, `files/commands.txt`)

MUST NOT:
- Produce `patch.diff` (ignored if present)

### 8.3 Role: check
Purpose: evaluate evidence vs acceptance criteria.

MUST:
- Produce `verdict.json`
- Produce `scorecard.md` (human-readable summary)
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
      "evidence": "files/test.log"
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

MUST NOT:
- Produce `patch.diff` (ignored if present)

### 8.4 Role: act
Purpose: propose patch to satisfy failed criteria.

MUST:
- Produce `output.json`
SHOULD:
- Produce `patch.diff` in unified diff format
- Explain in `summary` which ACs it targets and why

Rules:
- Agent does not apply the patch.
- Patch application is performed by norma.

---

## 9) Patch application (norma responsibility)

When a `patch.diff` is present:
- norma applies it atomically (preferred via git if available):
  - record git status/hash "before"
  - apply diff
  - record git status/hash "after"
- On apply failure:
  - rollback to "before" (best-effort)
  - mark step failed and stop (MVP)

Patch budgets (optional MVP):
- Reject patch if size > `max_patch_kb`.

---

## 10) Exec backend (MVP)

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
  - stop or continue depending on role (MVP: stop)

---

## 11) Codex oneshot backend (MVP)

Codex typically outputs free-form text. MVP requires deterministic output:

### Codex prompt policy (MUST)
norma generates a role-specific prompt that instructs Codex to:
- write required files for the role (check: verdict.json + scorecard.md; act: patch.diff)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir`
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

## 12) OpenCode oneshot backend (MVP)

OpenCode typically outputs free-form text. MVP requires deterministic output:

### OpenCode prompt policy (MUST)
norma generates a role-specific prompt that instructs OpenCode to:
- write required files for the role (check: verdict.json + scorecard.md; act: patch.diff)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir`
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

---

## 13) Gemini oneshot backend (MVP)

Gemini CLI typically outputs free-form text. MVP requires deterministic output:

### Gemini prompt policy (MUST)
norma generates a role-specific prompt that instructs Gemini to:
- write required files for the role (check: verdict.json + scorecard.md; act: patch.diff)
- output ONLY valid JSON for AgentResponse on stdout
- write only inside `step_dir`
- keep paths relative in `files[]`

### Capturing
- norma stores raw stdout/stderr to logs
- norma parses stdout as AgentResponse JSON
- if parse fails → protocol error

---

## 14) Acceptance checklist (MVP)

- [ ] `norma run <task-id>` creates a run and DB entry in `.norma/norma.db`
- [ ] Each step creates artifacts in `runs/<run_id>/steps/<n>-<role>/`
- [ ] Steps are committed atomically (tmp dir → rename; then DB transaction)
- [ ] check produces `verdict.json` + `scorecard.md`
- [ ] act can produce `patch.diff`, and norma applies it and records before/after snapshot
- [ ] Crash recovery cleans tmp dirs and reconciles missing DB step records
