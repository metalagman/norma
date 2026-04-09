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
- **AUTHORITATIVE Backlog (Beads):** Deeply integrated with [Beads](https://github.com/gastownhall/beads). Task state, structured work plans, and full run journals are persisted in Beads `notes`, synchronized via Git.
- **Intelligent Resumption:** Using granular labels like `norma-has-plan` and `norma-has-do`, norma can resume interrupted runs or skip already completed steps across different machines.
- **Pure-Go & CGO-Free:** Authoritative run state is managed via SQLite using the `modernc.org/sqlite` driver. Portable, fast, and easy to build.
- **Pluggable Agent Ecosystem:** Seamlessly mix and match agents using `generic_acp` binaries and standard ACP aliases (`codex_acp`, `opencode_acp`, `gemini_acp`, `copilot_acp`, `claude_code_acp`).
- **Ralph-Style Run Journal:** Persists structured per-step progress in task notes (`TaskState.journal`) for resumable run history.

---

## 🛠️ The Norma Loop

1.  **PLAN:** Refine the goal into a concrete `work_plan` and effective acceptance criteria.
2.  **DO:** Execute the plan. Agents modify code within the isolated workspace.
3.  **CHECK:** Evaluate the workspace against acceptance criteria and produce a `PASS/FAIL` verdict.
4.  **ACT:** If `PASS`, norma automatically merges and commits the changes to your main branch using **Conventional Commits**. If `FAIL`, the loop continues or prepares for a re-plan.

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
- **Go 1.25+**
- **bd** ([Beads CLI](https://github.com/gastownhall/beads)) installed in your PATH.
- **Git**

### 2. Install
```bash
go install github.com/normahq/norma/cmd/norma@latest
go install github.com/normahq/norma/cmd/relay@latest
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

The default configuration uses the `gemini_acp` agent. You can customize runtime core in `.norma/config.yaml` (or app-specific `.norma/<app>.yaml`):

```yaml
norma:
  agents:
    gemini_acp_agent:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
    opencode_acp_agent:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
    copilot_acp:
      type: copilot_acp
      copilot_acp:
        model: gpt-5-codex
    claude_code_acp_agent:
      type: claude_code_acp
      claude_code_acp:
        model: claude-sonnet-4-20250514
cli:
  pdca:
    plan: gemini_acp_agent
    do: gemini_acp_agent
    check: gemini_acp_agent
    act: gemini_acp_agent
  planner: gemini_acp_agent
  budgets:
    max_iterations: 5
  retention:
    keep_last: 50
    keep_days: 30
profiles:
  default:
    cli:
      pdca:
        plan: gemini_acp_agent
        do: gemini_acp_agent
        check: gemini_acp_agent
        act: gemini_acp_agent
      planner: gemini_acp_agent
  opencode:
    cli:
      pdca:
        plan: opencode_acp_agent
        do: opencode_acp_agent
        check: opencode_acp_agent
        act: opencode_acp_agent
      planner: opencode_acp_agent
  copilot:
    cli:
      pdca:
        plan: copilot_acp
        do: copilot_acp
        check: copilot_acp
        act: copilot_acp
      planner: copilot_acp
```

App configs can use app-specific environment prefixes (for example `RELAY_*` for relay settings).

## 📖 Documentation

- [Relay User Guide](cmd/relay/README.md)
- [Relay Technical Spec](docs/relay.md)
- [Planner and Interactive Planning](docs/planner.md)
- [PDCA Workflow and Norma Loop](docs/pdca-agent.md)
- [NormaLoop Orchestration](docs/normaloop-agent.md)
- [Codex ACP Bridge](docs/codex-acp-bridge.md)

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

### 6. Experimental ACP Playground
Use the playground ACP commands to talk to model CLIs through ACP and Go ADK without changing your normal Norma agent configuration.

```bash
# One-shot prompt
norma playground acp gemini --prompt "summarize this repository"

# Interactive REPL
norma playground acp gemini
```

Notes:
- This command is experimental and does not use `.norma/config.yaml` profiles.
- It starts Gemini CLI as `gemini --experimental-acp`.
- V1 is text-first: normal text responses are rendered in the terminal, while richer ACP events are mainly for debugging via `--debug-events`.

### 7. Codex ACP Proxy
Use the tool command to expose `codex mcp-server` as ACP over stdio.

```bash
# Start bridge with default ACP agent name
norma tool codex-acp-bridge

# Override ACP agent name
norma tool codex-acp-bridge --name team-codex

# Set Codex MCP `codex` tool config arguments
norma tool codex-acp-bridge --codex-model gpt-5.4 --codex-sandbox workspace-write
```

Notes:
- `--name` sets ACP `initialize.agentInfo.name`.
- `--codex-*` flags configure the initial MCP `codex` tool call.
- Full reference: [docs/codex-acp-bridge.md](docs/codex-acp-bridge.md).
- Standalone binary is also available as `codex-acp-bridge`.

### 8. Generic ACP Inspector (`acp-dump`)
Inspect any stdio ACP server command without changing Norma config.

```bash
# Human-readable summary
norma tool acp-dump -- opencode acp

# JSON output for scripts
norma tool acp-dump --json -- gemini --experimental-acp
```

Standalone binary is also available as `acp-dump`.

### 9. Generic MCP Inspector (`mcp-dump`)
Inspect any stdio MCP server command and dump capabilities plus MCP tool schemas.

```bash
# Human-readable summary
norma tool mcp-dump -- codex mcp-server

# JSON output for scripts
norma tool mcp-dump --json -- codex mcp-server
```

Standalone binary is also available as `mcp-dump`.

### 10. Generic ACP REPL (`acp-repl`)
Run an interactive terminal REPL against any stdio ACP server command.

```bash
norma tool acp-repl -- opencode acp
norma tool acp-repl --model opencode/big-pickle --mode coding -- opencode acp
norma tool acp-repl -- gemini --experimental-acp
```

Standalone binary is also available as `acp-repl`.

### 11. Omnidist Multi-Profile Distribution
Norma uses [Omnidist](https://github.com/metalagman/omnidist) profiles for build/stage/verify/publish flows across all command binaries.

Profiles configured in `.omnidist/omnidist.yaml`:
- `norma`
- `acp-dump`
- `mcp-dump`
- `acp-repl`
- `codex-acp-bridge`
- `relay`

npm distributions use the `@normahq` scope:
- `@normahq/norma`
- `@normahq/acp-dump`
- `@normahq/mcp-dump`
- `@normahq/acp-repl`
- `@normahq/codex-acp-bridge`
- `@normahq/relay`

Quickstart per profile:

```bash
omnidist --profile norma quickstart
omnidist --profile acp-dump quickstart
omnidist --profile mcp-dump quickstart
omnidist --profile acp-repl quickstart
omnidist --profile codex-acp-bridge quickstart
omnidist --profile relay quickstart
```

Run build pipeline for a profile:

```bash
omnidist --profile <profile> build
omnidist --profile <profile> stage
omnidist --profile <profile> verify
omnidist --profile <profile> npm publish
```

GitHub release workflows are split per profile and run on `v*` tag pushes:
- `omnidist-release-acp-dump.yml`
- `omnidist-release-mcp-dump.yml`
- `omnidist-release-acp-repl.yml`
- `omnidist-release-codex-acp-bridge.yml`
- `omnidist-release-relay.yml`

Publishing uses npm only.

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
