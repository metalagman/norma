# norma

<p align="center">
  <img src="docs/assets/norma_logo_300.png" alt="norma logo">
</p>

**norma** is a robust, autonomous agent workflow orchestrator written in Go. While built with Go's performance and reliability, it is designed to orchestrate development tasks for **any project**, regardless of the language or stack. 

norma bridges the gap between high-level task management and low-level code execution by enforcing a strict **Plan → Do → Check → Act (PDCA)** cycle.

Built for transparency and reliability, norma ensures every agent action is logged, every change is isolated in a Git worktree, and the entire run state is persisted directly within your backlog.

---

## 🚀 Key Highlights

- **Fixed PDCA Workflow:** A single, battle-tested loop: `Plan` the work, `Do` the implementation, `Check` the results, and `Act` on the verdict.
- **Isolated Git Workspaces:** Every run operates in a dedicated Git worktree on a task-scoped branch (`norma/task/<id>`). No more messy working trees or accidental commits.
- **AUTHORITATIVE Backlog (Beads):** Deeply integrated with [Beads](https://github.com/metalagman/beads). Task state, structured work plans, and full run journals are persisted in Beads `notes`, synchronized via Git.
- **Intelligent Resumption:** Using granular labels like `norma-has-plan` and `norma-has-do`, norma can resume interrupted runs or skip already completed steps across different machines.
- **Pure-Go & CGO-Free:** Authoritative run state is managed via SQLite using the `modernc.org/sqlite` driver. Portable, fast, and easy to build.
- **Pluggable Agent Ecosystem:** Seamlessly mix and match agents using `exec` binaries, CLI wrappers (`codex`, `opencode`, `gemini`, `claude`).
- **Ralph-Style Run Journal:** Persists structured per-step progress in task notes (`TaskState.journal`) for resumable run history.

---

## 🛠️ The Norma Loop

1.  **PLAN:** Refine the goal into a concrete `work_plan` and effective acceptance criteria.
2.  **DO:** Execute the plan. Agents modify code within the isolated workspace.
3.  **CHECK:** Evaluate the workspace against acceptance criteria and produce a `PASS/FAIL` verdict.
4.  **ACT:** If `PASS`, norma automatically merges and commits the changes to your main branch using **Conventional Commits**. If `FAIL`, the loop continues or prepares for a re-plan.

---

## 🚦 Supported Agents

Norma speaks a normalized JSON contract, allowing you to use any tool as an agent:

| Agent | Type | Description |
| :--- | :--- | :--- |
| **Exec** | `exec` | Run any local binary or script that handles JSON on stdin/stdout. |
| **Gemini** | `gemini` | Native support for the Gemini CLI with tool-calling and code-reading capabilities. |
| **Claude** | `claude` | Native support for the Claude CLI (Claude Code) for advanced reasoning and coding. |
| **OpenCode** | `opencode` | Deep integration with OpenCode for high-performance coding tasks. |
| **Codex** | `codex` | Optimized wrapper for OpenAI Codex-style CLI tools. |

---

## 🏁 Getting Started

### 1. Requirements
- **Go 1.25+**
- **bd** ([Beads CLI](https://github.com/metalagman/beads)) installed in your PATH.
- **Git**

### 2. Install
```bash
go install github.com/metalagman/norma/cmd/norma@latest
```

### 3. Initialize & Configure
Run `norma init` to automatically initialize `.beads` and create a default `.norma/config.yaml`:

```bash
norma init
```

The default configuration uses the `codex` agent with the `gpt-5.2-codex` model. You can customize it in `.norma/config.yaml`:

```yaml
profile: default

agents:
  codex_api:
    type: codex
    model: gpt-5.2-codex
  gemini_flash:
    type: gemini
    model: gemini-3-flash-preview

profiles:
  default:
    pdca:
      plan: codex_api
      do: gemini_flash
      check: codex_api
      act: codex_api
    planner: codex_api

budgets:
  max_iterations: 5
retention:
  keep_last: 50
  keep_days: 30
```

You can override config values through environment variables with the `NORMA_` prefix.

## 📖 Documentation

- [Planner and Interactive Planning](docs/planner.md)
- [PDCA Workflow and Norma Loop](docs/pdca-agent.md)
- [NormaLoop Orchestration](docs/normaloop-agent.md)
- [Codex ACP Proxy](docs/codex-acp-proxy.md)

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
Use `norma plan` to break a high-level epic into Beads epic/feature/task hierarchy. The planner is fully interactive and will ask you clarification questions using the `human` tool.

```bash
norma plan "Build multi-tenant billing and subscription management"
```

### 6. Experimental Gemini ACP Playground
Use the playground command to talk to Gemini CLI through ACP and Go ADK without changing your normal Norma agent configuration.

```bash
# One-shot prompt
norma playground gemini-acp --prompt "summarize this repository"

# Interactive REPL
norma playground gemini-acp
```

Notes:
- This command is experimental and does not use `.norma/config.yaml` profiles.
- It starts Gemini CLI as `gemini --experimental-acp`.
- V1 is text-first: normal text responses are rendered in the terminal, while richer ACP events are mainly for debugging via `--debug-events`.

### 7. Codex ACP Proxy
Use the proxy command to expose `codex mcp-server` as ACP over stdio.

```bash
# Start proxy with default ACP agent name
norma proxy codex-acp

# Override ACP agent name
norma proxy codex-acp --name team-codex

# Set Codex MCP `codex` tool config arguments
norma proxy codex-acp --codex-model gpt-5.4 --codex-sandbox workspace-write

# Forward additional flags to codex mcp-server
norma proxy codex-acp -- --trace --raw
```

Notes:
- `--name` sets ACP `initialize.agentInfo.name`.
- `--codex-*` flags configure the initial MCP `codex` tool call.
- All arguments after `--` are forwarded directly to `codex mcp-server`.
- Full reference: [docs/codex-acp-proxy.md](docs/codex-acp-proxy.md).

---

## 📊 State & Persistence

Norma ensures **Zero Data Loss**:
- **authoritative run state**: Stored in `.norma/norma.db` (SQLite).
- **Authoritative task state**: Serialized as a `TaskState` JSON object in Beads `notes`.
- **Artifacts**: Every step's `input.json`, `output.json`, and `logs/` are saved to disk under `.norma/runs/<run_id>/`.
- **Agent output visibility**: Agent `stdout`/`stderr` is always captured in step logs and is mirrored to terminal only when running with `--debug`.

---

## 🤝 Contributing

We welcome contributions! Whether it's adding new agent wrappers, improving the scheduler, or refining the PDCA logic, please feel free to open an issue or submit a PR.

*Note: norma follows the [Conventional Commits](https://www.conventionalcommits.org/) specification.*

---

## 📜 License

MIT License. See [LICENSE](LICENSE) for details.
