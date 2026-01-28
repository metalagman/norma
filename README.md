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
- **Pluggable Agent Ecosystem:** Seamlessly mix and match agents using `exec` binaries or dedicated wrappers for `codex`, `opencode`, and `gemini` CLIs.
- **Ralph-Style Run Journal:** Automatically reconstructs and maintains `artifacts/progress.md`, providing a human-readable timeline of every step taken.

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
Run `norma init` to automatically initialize `.beads` and create a default `.norma/config.json`:

```bash
norma init
```

The default configuration uses the `codex` agent with the `gpt-5.2-codex` model. You can customize it in `.norma/config.json`:

### 4. Create a Task & Run
```bash
# Add a task to Beads
norma task add "implement user logout" --ac "/logout returns 200"

# Orchestrate the fix
norma loop norma-a3f2dd
```

---

## 📊 State & Persistence

Norma ensures **Zero Data Loss**:
- **authoritative run state**: Stored in `.norma/norma.db` (SQLite).
- **Authoritative task state**: Serialized as a `TaskState` JSON object in Beads `notes`.
- **Artifacts**: Every step's `input.json`, `output.json`, and `logs/` are saved to disk under `.norma/runs/<run_id>/`.

---

## 🤝 Contributing

We welcome contributions! Whether it's adding new agent wrappers, improving the scheduler, or refining the PDCA logic, please feel free to open an issue or submit a PR.

*Note: norma follows the [Conventional Commits](https://www.conventionalcommits.org/) specification.*

---

## 📜 License

MIT License. See [LICENSE](LICENSE) for details.