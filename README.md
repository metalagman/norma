# norma

norma is a minimal agent workflow runner for Go projects. It orchestrates a fixed `plan → do → check → act` loop, persists authoritative run state in SQLite (via the pure-Go `modernc.org/sqlite` driver), and writes every artifact to disk so humans can inspect and debug runs.

## Highlights

- **Single workflow.** Every run iterates over `plan`, `do`, `check`, and `act` steps until the acceptance criteria pass or a budget (such as `max_iterations`) is exhausted.
- **Artifacts on disk.** Agents only write inside their step directory; logs, plans, evidence, verdicts, patches, and other files live under `.norma/runs/<run_id>/steps/<NNN-role>/`.
- **SQLite state, no CGO.** Run metadata, timelines, and key/value state live in `.norma/norma.db`. Connections enforce `foreign_keys=ON`, `journal_mode=WAL`, and `busy_timeout=5000` for durability while keeping builds CGO-free.
- **Pluggable agents.** Each role is backed by either an `exec` binary or a Codex CLI invocation. Agents speak a normalized JSON contract so you can mix and match implementations.
- **Atomic commits & recovery.** Step artifacts are written to a temp dir, renamed atomically, and then committed inside a DB transaction. On startup norma cleans stray temp dirs and reconciles missing records.
- **Task graph tooling.** The `norma task` subcommands let you capture goals, link dependencies, and trigger runs from the queue or its leaf nodes.

## Getting Started

1. **Requirements.** Go 1.22 or newer, `bd` (beads CLI) executable in PATH, and a writable working tree (norma only needs filesystem + SQLite).
2. **Build/install.**

   ```bash
   go install ./cmd/norma
   # or run in-place
   go run ./cmd/norma --help
   ```

3. **Create `.norma/config.json`.** See the configuration section below for an example that wires up agents and budgets.
4. **Add a task and run it.**

   ```bash
   norma task add "ship initial README" \
     --ac "AC1: README explains workflow" \
     --ac "AC2: Document config format"
   # suppose it prints "task 12 added"
   norma run 12
   ```

   norma creates `.norma/runs/<run_id>/`, spawns the configured agents in order, and stops when the check step returns `PASS`, an agent fails, or a budget stops the run.

## Configuration & Agents

Configuration lives in `.norma/config.json` (overridable via `--config`). It declares one agent per role plus global budgets:

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
    "max_patch_kb": 200,
    "max_changed_files": 20,
    "max_risky_files": 5
  },
  "retention": {
    "keep_last": 50,
    "keep_days": 30
  }
}
```

- `exec` agents receive `AgentRequest` JSON on stdin and must emit `AgentResponse` JSON on stdout.
- `codex`, `opencode`, and `gemini` agents wrap their respective CLIs. norma uses the fixed tool binary name (no `cmd` override) and constrains their working directory (`path`) while enforcing JSON-only stdout.
- Budgets cap iterations and patch size/width (`max_patch_kb`, `max_changed_files`). `max_risky_files` is reserved for future heuristics. `max_iterations` is required.
- Retention prunes old runs on every `norma run`. Set `keep_last`, `keep_days`, or both.

## Agent Contract

Each step receives a normalized request (paths are absolute, but the agent may only write inside `step_dir`):

```json
{
  "version": 1,
  "run_id": "20260123-145501-ab12cd",
  "step": { "index": 2, "role": "check", "iteration": 1 },
  "goal": "Short human goal",
  "norma": {
    "acceptance_criteria": [{ "id": "AC1", "text": "All unit tests pass" }],
    "budgets": { "max_iterations": 5, "max_patch_kb": 200 }
  },
  "paths": {
    "repo_root": "/abs/path/to/repo",
    "run_dir": "/abs/path/.norma/runs/20260123-145501-ab12cd",
    "step_dir": "/abs/path/.../steps/003-check"
  },
  "context": { "artifacts": ["/abs/path/.../steps/001-plan/plan.md"], "notes": "Optional" }
}
```

Agents reply with:

```json
{
  "version": 1,
  "status": "ok",
  "summary": "One-line outcome",
  "files": ["files/test.log"],
  "next_actions": ["Re-run golangci-lint"],
  "errors": []
}
```

- `status` is `ok` or `fail`. On `fail`, norma stops the run and records the summary.
- Paths in `files[]` are relative to the step directory.
- Role-specific expectations:
  - `plan`: optional `plan.md` outline.
  - `do`: evidence under `files/`.
  - `check`: **must** write `verdict.json` (`PASS` or `FAIL`) and `scorecard.md`.
  - `act`: should write `patch.diff` describing the proposed fix; norma is responsible for applying it.

## Workflow & Artifacts

Every step is materialized under `.norma/runs/<run_id>/steps/` using the `NNN-role` naming scheme. A typical layout:

```
.norma/
  norma.db
  runs/20260123-145501-ab12cd/
    norma.md                # Goal, acceptance criteria, and budgets
    steps/
      001-plan/
        input.json
        output.json
        logs/stdout.txt
        logs/stderr.txt
        plan.md

## Sane Norma Loop (PDCA)

- **PLAN:** update ordered backlog, pick a Next Slice (1 feature + 1–3 tasks), define stop conditions and what Check must verify.
- **DO:** execute only the slice and produce artifacts. If scope grows or uncertainty appears, create a Spike or split tasks—don’t expand scope inside Do.
- **CHECK:** run verifications for the slice (tests/lint/build/AC checks/diff review) and classify pass/partial/fail.
- **ACT:** update backlog and decisions, then choose continue, re-plan, rollback, or ship.

Invariants: bounded work, always verifiable tasks, backlog as the source of truth. Stop conditions include scope growth to L, missing requirements, blockers, systemic test issues, or repeated retries.
      002-do/
        input.json
        output.json
        logs/
        files/commands.txt
      003-check/
        input.json
        output.json
        verdict.json
        scorecard.md
        logs/
      004-act/
        input.json
        output.json
        patch.diff
        logs/
```

Key rules:

- Step directories are first created with a `.tmp-<rand>` suffix. Once the agent finishes, norma renames the directory atomically and then records the step in the database.
- Agents must never write outside `step_dir`; repo files remain read-only except for norma applying an `act` patch.
- `norma.md` captures the run goal/budgets for quick human reference.

## SQLite Storage

- Database path: `.norma/norma.db`.
- Opened with `SetMaxOpenConns(1)` and `SetMaxIdleConns(1)` to serialize writes.
- Required PRAGMAs: `foreign_keys=ON`, `journal_mode=WAL` (logged if unavailable), `busy_timeout=5000` ms.
- Schema overview (managed by `pressly/goose` migrations):
  - `schema_migrations`: simple version tracker.
  - `runs`: run metadata, status, iteration counters, and run directory.
  - `steps`: one row per committed step (primary key `(run_id, step_index)`).
  - `events`: append-only timeline for UI/debugging.
  - `kv_run`: optional JSON blobs per run.

## Task Graph Workflow (Beads)

norma uses the `beads` tool for task management. Typical flow:

```bash
norma task add "Tighten lint config" --ac "AC1: golangci-lint passes"
norma task list --status todo
norma task link 12 --depends-on 7 --depends-on 9
norma run 12          # run a specific task
norma run             # run all leaf TODO tasks (ready in beads)
```

- Tasks are stored in `.beads/` as JSONL files.
- `norma` interacts with `beads` via the `bd` CLI.
- Epics, Features, and Tasks are supported.
- `run` (with no task id) pulls leaf tasks that are "ready" in beads.
- To retry a failed/stopped task, run it explicitly by id (`norma run <task-id>`).
- On startup, norma recovers `doing` tasks whose runs are not active, marking them `failed` so you can retry.

Manual run pruning:

```bash
norma runs prune --keep-last 20
norma runs prune --keep-days 14
norma runs prune --keep-last 50 --keep-days 30 --dry-run
```

## Development Tips

- Run tests before shipping changes:

  ```bash
  go test ./...
  ```

- Use `norma run --debug 12` (or `norma run --debug` for leaf runs) to enable zerolog debug output during a run.
- Keep agents deterministic: emit only JSON to stdout and store any prose or evidence in files under `step_dir` so norma can parse responses reliably.
