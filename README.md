# norma

<p align="center">
  <img src="docs/assets/norma_logo_300.png" alt="norma logo">
</p>

**norma** is a robust, autonomous agent workflow orchestrator written in Go. While built with Go's performance and reliability, it is designed to orchestrate development tasks for **any project**, regardless of the language or stack.

norma bridges the gap between high-level task management and low-level code execution with a strict core orchestration mode:

- a strict **Plan -> Do -> Check -> Act (PDCA)** cycle for one task at a time

The repository also contains experimental root commands for swarm and app harnesses. Those surfaces are available for internal evaluation, but PDCA is the supported core workflow.

Built for transparency and reliability, norma ensures every agent action is logged, every change is isolated in a Git worktree, and durable implementation history lives on a task-scoped branch.

---

## 🚀 Key Highlights

- **Fixed PDCA Workflow:** A strict execution loop for one task: `Plan`, `Do`, `Check`, `Act`.
- **Experimental Swarm Harness:** Route ready Beads tasks to configured role agents by `assignee`, with Beads state as the control surface.
- **Isolated Git Workspaces:** Every run operates in a dedicated Git worktree on a task-scoped branch (`norma/task/<id>`). No more messy working trees or accidental commits.
- **AUTHORITATIVE Backlog (Beads):** Deeply integrated with [Beads](https://github.com/gastownhall/beads). Task status and workflow phase labels stay in Beads; implementation progress stays in Git.
- **Branch-First Recovery:** Interrupted runs are retried from the task branch (`norma/task/<id>`) instead of using persisted role state in Beads notes.
- **Pure-Go & CGO-Free:** Authoritative run state is managed via SQLite using the `modernc.org/sqlite` driver. Portable, fast, and easy to build.
- **Pluggable Agent Ecosystem:** Seamlessly mix and match agents using `generic_acp` binaries and standard ACP aliases (`codex_acp`, `opencode_acp`, `gemini_acp`, `copilot_acp`, `claude_code_acp`).
- **Inspectable Run Artifacts:** Persists step inputs, outputs, logs, and event streams under `.norma/runs/` for local debugging.

---

## 🛠️ Workflows

### PDCA

1. **PLAN:** Refine the goal into concrete `do_steps` and acceptance criteria checks.
2. **DO:** Execute the plan. Agents modify code within the isolated workspace.
3. **CHECK:** Evaluate the workspace against acceptance criteria and produce a verdict: `pass` or `fail`.
4. **ACT:** Choose the next action from that verdict. `pass` must use `decision=close`, which lets Norma merge and commit changes using **Conventional Commits**. `fail` uses `decision=continue` to retry or `decision=replan` to create replacement work.

### Experimental Swarm

1. Select ready Beads leaf tasks under one epic.
2. Route each task by `assignee` to a configured role agent.
3. Let role agents coordinate through `norma.tasks.*` MCP tools.
4. Infer completion or handoff from Beads state after each session.

---

## 🚦 Supported Agents

Norma speaks a normalized JSON contract and utilizes the **Agent Control Protocol (ACP)** for tool-calling and code execution:

| Agent | Type | Description |
| :--- | :--- | :--- |
| **Generic** | `generic_acp` | Run any local binary or script that implements the Agent Control Protocol. |
| **Gemini** | `gemini_acp` | Native support for the Gemini CLI with tool-calling and code-reading capabilities. |
| **OpenCode** | `opencode_acp` | Deep integration with OpenCode for high-performance coding tasks. |
| **Codex** | `codex_acp` | Optimized bridge for OpenAI Codex-style CLI tools via Norma's Codex ACP bridge. |
| **Copilot** | `copilot_acp` | Runs Copilot CLI in ACP mode via `copilot --acp`. |
| **Claude Code** | `claude_code_acp` | Runs Claude Code ACP via `npx -y @zed-industries/claude-code-acp@latest`. |

---

## 🏁 Getting Started

### 1. Requirements
- **Go 1.26+**
- **bd** ([Beads CLI](https://github.com/gastownhall/beads)) installed in your PATH.
- **Git**

### 2. Install
```bash
go install github.com/normahq/norma/v2/cmd/norma@latest
```

### 3. Initialize & Configure
Run `norma init` to automatically initialize `.beads` and create a default `.norma/config.yaml`:

```bash
norma init
```

### Global Flags

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config-dir` | string | `""` | Extra config root directory (highest priority) |
| `--debug` | bool | `false` | Enable debug logging |
| `--trace` | bool | `false` | Enable trace logging (overrides --debug) |
| `--profile` | string | `""` | Config profile name |

The default configuration uses an OpenCode ACP provider named `opencode`. You can customize runtime core in `.norma/config.yaml` (or app-specific `.norma/<app>.yaml`):

```yaml
runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
    codex:
      type: codex_acp
      codex_acp:
        model: gpt-5-codex
    claude_code:
      type: claude_code_acp
      claude_code_acp:
        model: claude-sonnet-4-20250514
    copilot:
      type: copilot_acp
      copilot_acp:
        model: gpt-5-codex
    gemini:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
cli:
  pdca:
    plan: opencode
    do: opencode
    check: opencode
    act: opencode
  budgets:
    max_iterations: 5
  retention:
    keep_last: 50
    keep_days: 30
planner:
  provider: opencode
swarm:
  primary_role: coordinator
  default_provider: opencode
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: Decide routing, resolve bounced tasks, supervise swarm progress.
    planner:
      assignee: norma-planner
      instruction: Break down work and assign tasks to roles.
    implementer:
      assignee: norma-implementer
      instruction: Implement assigned tasks.
profiles:
  default:
    cli:
      pdca:
        plan: opencode
        do: opencode
        check: opencode
        act: opencode
    planner:
      provider: opencode
    swarm:
      default_provider: opencode
  opencode:
    cli:
      pdca:
        plan: opencode
        do: opencode
        check: opencode
        act: opencode
    planner:
      provider: opencode
    swarm:
      default_provider: opencode
  codex:
    cli:
      pdca:
        plan: codex
        do: codex
        check: codex
        act: codex
    planner:
      provider: codex
    swarm:
      default_provider: codex
  claude_code:
    cli:
      pdca:
        plan: claude_code
        do: claude_code
        check: claude_code
        act: claude_code
    planner:
      provider: claude_code
    swarm:
      default_provider: claude_code
  copilot:
    cli:
      pdca:
        plan: copilot
        do: copilot
        check: copilot
        act: copilot
    planner:
      provider: copilot
    swarm:
      default_provider: copilot
```

## 📖 Documentation

- [Planner and Interactive Planning](docs/planner.md)
- [PDCA Workflow and Norma Loop](docs/pdca-agent.md)
- [NormaLoop Orchestration](docs/normaloop-agent.md)
- [Swarm Harness](docs/swarm.md)
- [Experimental Goalkeeper Command](docs/goalkeeper.md)
- [Experimental Goalkeeper Actor Command](docs/goalkeeper-actor.md)
- [Experimental Taskmaster Commands](docs/taskmaster.md)
- [Actorlayer Runtime (Actor Model over ADK)](docs/actorlayer.md)

### 4. Create a Task & Run
```bash
# Add a task to Beads
bd create --type task \
  --title "implement user logout" \
  --description $'Objective: implement user logout\nArtifact: auth/logout handler and tests\nVerify:\n- go test ./...'

# Orchestrate the fix
norma loop norma-a3f2dd
```

### 5. Decompose a Global Epic
Use `norma plan tui` to break a high-level epic into Beads epic/feature/task hierarchy.

```bash
norma plan tui   # interactive TUI
norma plan repl  # line-based REPL
```

### 6. Run a Swarm Epic

Use `norma swarm <epic-id>` for internal evaluation when work is already broken into Beads tasks and routed by assignee. This is not part of the core PDCA MVP surface.

```bash
norma swarm norma-phmp
```

Swarm notes:

- initial assignment is human-owned
- unassigned ready tasks are skipped and reported
- agents may create and reassign tasks through `norma.tasks.*`
- Beads task state is the source of truth for completion and handoff

### 7. Codex ACP Bridge (External Package)
Codex ACP bridge is now distributed as a standalone package.

```bash
npx -y @normahq/codex-acp-bridge@1.6.3
```

Notes:
- `codex_acp` agent type resolves to `npx -y @normahq/codex-acp-bridge@1.6.3`.
- Per-session Codex defaults are set via ACP `session/new._meta.codex`.
- Bridge docs live in the standalone repository:
  <https://github.com/normahq/codex-acp-bridge>

### 8. Standalone Protocol Tools

Protocol inspection and REPL helpers are distributed as standalone tools:

- `acp-dump`: <https://github.com/normahq/acp-dump>
- `mcp-dump`: <https://github.com/normahq/mcp-dump>
- `acp-repl`: <https://github.com/normahq/acp-repl>

Install with npm:

```bash
npm install -g @normahq/acp-dump@latest
npm install -g @normahq/mcp-dump@latest
npm install -g @normahq/acp-repl@latest
```

---

## 📊 State & Persistence

Norma ensures **Zero Data Loss**:
- **authoritative run state**: Stored in `.norma/norma.db` (SQLite).
- **authoritative task state**: Stored in Beads status and workflow phase labels.
- **durable implementation history**: Stored in the task branch (`norma/task/<id>`).
- **Artifacts**: Every step's `input.json`, `output.json`, and `logs/` are saved to disk under `.norma/runs/<run_id>/`.
- **Agent output visibility**: Agent `stdout`/`stderr` is always captured in step logs and is mirrored to terminal only when running with `--debug`.

---

## 🤝 Contributing

We welcome contributions! Whether it's adding new agent wrappers, improving the scheduler, or refining the PDCA logic, please feel free to open an issue or submit a PR.

*Note: norma follows the [Conventional Commits](https://www.conventionalcommits.org/) specification.*

---

## 📜 License

MIT License. See [LICENSE](LICENSE) for details.
