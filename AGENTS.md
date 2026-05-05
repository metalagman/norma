# norma — agents.md (MVP Spec, SQLite no-CGO)

This document defines the **MVP agent interface** for `norma` (written in Go) and the **MVP storage model** using **SQLite without CGO** (pure-Go driver), while keeping **artifacts on disk** and **run/step state in DB**. **Task state and backlog are Beads-only** and must not be mirrored in Norma state.

Single fixed workflow:
> `plan → do → check → act` (loop until `verdict=pass` + `decision=close`, or until a stop condition triggers)

---

## 0) Design principles

- **One workflow**; flexibility comes from swapping agents per role.
- **Google ADK required:** all orchestrator and role agents MUST be implemented using `google.golang.org/adk`.
- **Artifacts are files** (human-debuggable).
- **Run/step state is in SQLite** (queryable, UI-friendly).
- **Task state lives in Beads** (source of truth for progress and resumption).
- **Ephemeral TaskState:** `TaskState` is ADK session state for role-to-role handoff within one live run. Beads `notes` MUST NOT be used as Norma machine state.
- **Workspaces (Git Worktrees):** Every PDCA run MUST operate in one shared Git worktree located at `<run_dir>/workspace`. Agents perform all workspace reads/writes within this isolated workspace.
- **Step summaries:** Step summaries are stored in SQLite step records and step artifacts under `.norma/runs/`.
- **Task-scoped Branches:** Workspaces use Git branches scoped to the task: `norma/task/<task_id>`. This allows progress to be restartable across multiple runs.
- **Workflow State in Labels:** Granular phase labels (`planning`, `doing`, `checking`, `acting`) are used for visibility only. Completed-step skip labels (`norma-has-plan`, `norma-has-do`, `norma-has-check`) MUST NOT be used.
- **Git History as Source of Truth:** The orchestrator extracts changes from the workspace using Git (e.g., `git merge --squash`).
- **Any agent** is supported through a **normalized JSON contract**.

## 0.1) Agent guidelines (for Norma development)

When working ON the `norma` project itself, agents MUST:
- **Follow Google Go Best Practices**: Adhere strictly to the principles and idioms defined in the Google Go Style Guide and Best Practices.
- **Use project-local tools**: Always prefer project-local tools via `go tool` (e.g., `go tool golangci-lint run`).
- **Always verify changes**: MUST run tests using `go test -race ./...` and linters (`go tool golangci-lint run`) before submitting code changes. Never assume code is correct without passing local quality gates.

## 0.2) Contributing requirements

All contributors MUST:
- **Start with a tracked issue**: File or reference a Beads issue before opening a PR.
- **Follow Conventional Commits**: Use Conventional Commits for all new commits.
- **Pass quality gates**: `go test -race ./...` and `go tool golangci-lint run` must pass before submission. **Always run these quality gates after making any changes to ensure stability.**
- **Sync via merge (no rebase)**: Use merge-based pulls when updating from origin (`git pull --no-rebase` or `git pull --merge`). Do not rebase shared branches.
- **No new tests for pure removals**: When removing functionality, do not add new tests as part of that removal.

## 0.3) Logging policy

To ensure consistent and high-quality logging across all components:
- **Zerolog and slog**: All components MUST use `github.com/rs/zerolog` or `log/slog` for logging.
- **Prohibited libraries**: Usage of `logrus`, `zap`, or the standard `log` package is strictly prohibited.
- **Global configuration**: Use the global logger initialized via `internal/logging.Init()`.
- **Structured logging**: Prefer structured logging (e.g., `log.Info().Str("key", value).Msg("message")`) over formatted strings.

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
      workspace/              # shared Git worktree for this run
      steps/
        01-plan/
          input.json
          output.json
          artifacts/
          logs/
            stdout.txt
            stderr.txt
        02-do/
          input.json
          output.json
          artifacts/
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
- **Workspaces:** Every PDCA run gets one Git worktree in `<run_dir>/workspace`. Agents perform all workspace reads/writes inside this shared run workspace. The orchestrator tracks changes by inspecting the Git history/diff of the task branch.
- **No task state in Norma DB:** task status, priority, dependencies, and selection are managed in Beads only.
- **Artifacts:** The `artifacts/` directory contains all artifacts produced during the run. Agents MUST write their artifacts here and MAY read existing artifacts from here.
- Agents MUST only write inside their current `step_dir` for logs/metadata, the shared run `workspace/`, and the shared `artifacts/` directory.

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
- `verdict TEXT NULL`                 (`pass|fail`)
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

Run the **single fixed** Norma workflow: **Plan → Do → Check → Act** until `verdict=pass` + `decision=close`, or until a **stop condition** triggers.

### Core invariants

1. **One workflow only:** `plan -> do -> check -> act` (repeat).
2. **Workspace exists before any agent runs:** the orchestrator creates `<run_dir>/workspace/` before the first role step. This workspace is a dedicated Git worktree shared by all PDCA roles in the run.
3. **Agents never modify workspace or git directly except for file writes in Do:** all agents operate in **read-only** mode with respect to `workspace/` by default. Using `git` commands from within agents is **STRICTLY FORBIDDEN**.
4. **Orchestrator commits changes in Do:** If the Do agent finishes with status `ok`, the orchestrator is responsible for staging and committing all changes in the `workspace/` Git repository.
5. **Commits/changes in main repo happen outside agents:** the orchestrator extracts changes from the task branch and applies them to the main repository.
6. **Contracts are JSON only:** every step is `input.json → output.json`.
7. **Every step MUST produce output.json:** The agent MUST write its AgentResponse JSON to `output.json` in the step directory.
8. **Every step captures logs:**
    - `steps/<n>-<role>/logs/stdout.txt`
    - `steps/<n>-<role>/logs/stderr.txt`
   - Agent `stdout`/`stderr` MUST be mirrored to terminal only when debug mode is enabled.
9. **Step summaries:** the orchestrator records step summaries in SQLite events and step artifacts.
10. **Acceptance criteria (AC):** baseline ACs are passed into Plan; Plan may extend them with traceability.
11. **Check compares plan vs actual and verifies job done:** Check must compare planned Do steps to executed Do step IDs and evaluate all acceptance criteria.
12. **Verdict goes to Act:** Act receives Check verdict and emits a legal decision for that verdict.
13. **Agents receive `<run_dir>/workspace` as their working directory through session state and `paths.workspace_dir`; step directories remain artifact/log locations.**

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

**Workflow State in Labels:** Granular workflow states (`planning`, `doing`, `checking`, `acting`) are tracked using `bd` labels on the task for visibility. Exactly one phase label should be present during a running step, and terminal outcomes clear all phase labels.

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
- live ADK session `TaskState` from prior steps in the same run, if any
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
- `pass` / `fail`
- Evidence (test output summary, commands run, links to artifacts)

### 4) ACT (Act Agent + Orchestrator)
The Act agent emits the decision. The orchestrator keeps `TaskState` in ADK session state only for the live run and applies the decision. It does not persist Norma machine state to Beads notes.

Control literals:
- Check verdicts are lowercase: `pass`, `fail`.
- Act decisions are lowercase: `close`, `continue`, `replan`.
- Run statuses are lowercase: `passed`, `failed`, `stopped`.

Legal verdict/decision pairs:
- `pass` + `close`: task is complete.
- `fail` + `continue`: retry the next PDCA iteration from the task branch.
- `fail` + `replan`: stop this task and create replacement/follow-up work.

Invalid Act outputs:
- `pass` + `continue`
- `pass` + `replan`
- `fail` + `close`
- any `rollback` decision

If `pass` + `close`:
- Close `next_task_id`.
- Extract changes from `workspace/` and apply to main repository using `git merge --squash`.
- Create a Conventional Commit.

If `fail` + `continue`:
- Return the task to open for retry from the task branch.
- The PDCA loop continues to the next iteration unless budgets are exhausted.

If `fail` + `replan`:
- Create and link replacement/follow-up work.
- Close the old task with a replan reason.
- End the current run with status `failed`.

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

## 6) Agent system (Google ADK + ACP)

All agents in Norma MUST be authored as Google ADK agents and utilize the **Agent Control Protocol (ACP)** for communication. Agents are executed as ephemeral runtimes per PDCA step and wrapped with a **structured I/O layer** that ensures compliance with role-specific JSON contracts.

### 6.1 Agent types (MVP)
- `generic_acp`: ADK agent adapter that spawns a local binary implementing the Agent Control Protocol.
- `gemini_acp`: Standard alias for the Gemini CLI implementing ACP.
- `opencode_acp`: Standard alias for the OpenCode CLI implementing ACP.
- `codex_acp`: Standard alias for the Codex proxy implementing ACP.

### 6.2 Agent configuration (MVP)
Stored in `.norma/config.yaml`.

Example:
```yaml
profile: default

agents:
  gemini_agent:
    type: gemini_acp
    model: gemini-3-flash-preview
  opencode_agent:
    type: opencode_acp
    model: opencode/big-pickle

profiles:
  default:
    pdca:
      plan: gemini_agent
      do: opencode_agent
      check: gemini_agent
      act: gemini_agent
    planner: gemini_agent

budgets:
  max_iterations: 5

retention:
  keep_last: 50
  keep_days: 30
```

Notes:
- Every configured role agent MUST be instantiated and executed through ADK (`agent.Agent` + ADK runner).
- The orchestrator creates a fresh agent instance for every PDCA step.
- The `structured` ADK wrapper handles mapping of JSON input/output and schema validation.
- `profiles.<name>.pdca.*` and `profiles.<name>.planner` must reference keys defined in top-level `agents`.
- `retention.keep_last` and `retention.keep_days` control auto-pruning on each run (optional).

---

## 7) Agent contracts (JSON)

Every step is an `input.json → output.json` transformation. The agent MUST produce an `output.json` file in the assigned step directory containing the valid AgentResponse JSON.

Contracts are formally defined by JSON schemas located in `internal/agents/pdca/roles/<role>/*.schema.json`.

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
    "goal": "...",
    "acceptance_criteria": [
      { "id": "AC-1", "text": "...", "verify_hints": ["..."] }
    ]
  },
  "step": {
    "index": 1
  },
  "paths": {
    "workspace_dir": "workspace"
  }
}
```

### 7.2 Common output.json (all steps)

All agent outputs share a common structure, extended with role-specific fields.

```json
{
  "status": "ok|stop|error",
  "stop_reason": "none|budget_exceeded|dependency_blocked|verify_missing|replan_required",
  "summary": "short human summary"
}
```

---

## 8) Role-specific requirements (Step Requirements)

### 8.1 Role: 01-plan

Plan **must**:
- produce `do_steps` for the iteration
- publish `acceptance_criteria` with verification checks

Plan `output.json` must include:

```json
{
  "plan_output": {
    "acceptance_criteria": [
      {
        "id": "AC-1",
        "text": "Unit tests pass",
        "checks": [
          { "id": "CHK-AC-1-1", "command": "go test -race ./...", "expected_exit_codes": [0] }
        ]
      }
    ],
    "do_steps": [
      { "id": "DO-1", "text": "Run unit tests" }
    ]
  }
}
```

### 8.2 Role: 02-do

Do **must**:
- execute only `plan_output.do_steps[*]`
- record what was executed (actual work)

Do `input.json` must include:
- `do_input.do_steps`
- `do_input.acceptance_criteria`

Do `output.json` must include:

```json
{
  "do_output": {
    "executed_step_ids": ["DO-1"]
  }
}
```

### 8.3 Role: 03-check

Check **must**:
1) verify **plan match** (planned vs executed)
2) verify **job done** (all acceptance criteria evaluated)
3) emit a verdict used by Act

Check `input.json` must include:
- `check_input.do_steps`
- `check_input.acceptance_criteria`
- `check_input.executed_step_ids`

Check `output.json` must include:

```json
{
  "check_output": {
    "acceptance_results": [
      {
        "ac_id": "AC-1",
        "result": "pass|fail",
        "notes": "..."
      }
    ],
    "verdict": "pass|fail"
  }
}
```

#### Verdict rules (enforceable)
- If any planned Do step was not executed → `verdict = "fail"`.
- If any `acceptance_results[*].result == "fail"` → `verdict = "fail"`.
- Else → `verdict = "pass"`.

### 8.4 Role: 04-act

Act **must**:
- consume Check verdict
- decide what to do next

Act `input.json` must include:
- `act_input.verdict` (and optionally `act_input.acceptance_results`)

Act `output.json` must include:

```json
{
  "act_output": {
    "decision": "close|continue|replan"
  }
}
```

#### Decision rules (enforceable)
- If `act_input.verdict == "pass"` → `decision = "close"`.
- If `act_input.verdict == "fail"` → `decision = "continue"` or `decision = "replan"`.
- `decision = "rollback"` is not valid PDCA output.

---

## 9) Applying Changes (norma responsibility)

norma extracts changes from the shared run workspace using Git:
- When a run reaches `verdict=pass` + `decision=close`, norma extracts changes from the task branch workspace (e.g., via `git diff HEAD`).
- norma applies the captured changes to the main repository atomically:
  - record git status/hash "before"
  - apply changes
  - if successful, commit changes using **Conventional Commits** format:
    - `fix: <summary>` or `feat: <summary>` based on the goal/task
    - Include `run_id` and `step_index` in the commit footer
  - record git status/hash "after"
- On apply failure:
  - restore to "before" (best-effort)
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

## 11) Acceptance checklist (MVP)

- [x] `norma init` initializes .beads, .norma directory and default config.yaml
- [x] `norma loop <task-id>` creates a run and DB entry in `.norma/norma.db`
- [x] Each run creates an isolated Git worktree at `<run_dir>/workspace/`
- [x] Each run uses a task-scoped Git branch: `norma/task/<task_id>`
- [x] Workflow states are tracked via `bd` labels on the task
- [x] Each step records structured summaries in SQLite events and step artifacts
- [x] Each step creates artifacts (`input.json`, `output.json`, `logs/`) in `runs/<run_id>/steps/<n>-<role>/`
- [x] Successful runs extract changes from the shared run workspace and apply them to the main repo
- [x] Crash recovery cleans tmp dirs and reconciles missing DB step records

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - `go test -race ./...`, `go tool golangci-lint run`, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --no-rebase
   bd dolt push
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

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd dolt push
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
<!-- END BEADS INTEGRATION -->
