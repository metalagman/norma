# PDCA Loop

This document describes Norma's fixed execution loop:

`plan -> do -> check -> act`

The loop repeats until the task is completed (`PASS` + `act.decision=close`) or a stop condition is reached.

## Scope

- Single workflow only: no alternative orchestration graph.
- One task at a time per run.
- Each step is contract-driven: `input.json -> output.json`.

## Iteration Flow

### 1) Plan

Purpose:
- refine the selected task into an executable plan for this iteration
- define effective acceptance criteria and verification checks

Expected output:
- `plan_output.acceptance_criteria.effective`
- `plan_output.work_plan.do_steps`
- `plan_output.work_plan.check_steps`

### 2) Do

Purpose:
- execute only planned implementation steps
- produce artifacts and code changes inside step workspace

Expected output:
- `do_output.execution.executed_step_ids`
- `do_output.execution.skipped_step_ids`

### 3) Check

Purpose:
- verify plan-vs-execution match
- verify effective acceptance criteria
- produce verdict

Expected output:
- `check_output.acceptance_results`
- `check_output.verdict.status` (`PASS|FAIL|PARTIAL`)

Verdict rules:
- Any failed acceptance result -> `FAIL`
- Otherwise verdict is derived by the Check agent and recorded in `check_output.verdict`.

Current schema note:
- `check_output.plan_match` is not part of the current `check/output.schema.json`.
- `do_output.execution.commands` is not part of the current `do/output.schema.json`.

### 4) Act

Purpose:
- decide what happens next from check verdict

Expected output:
- `act_output.decision` (`close|replan|rollback|continue`)

Act behavior:
- `close` with effective `PASS`: task is closed and changes are applied to main repo.
- `replan|continue`: task remains open and loop may continue or stop by policy.
- `rollback` (loop mode): Norma drops the task branch `norma/task/<task_id>`, resets task workflow state to `todo`, then continues with normal scheduler selection.

## Workspaces and Artifacts

- Every step runs in its own step directory:
  - `.norma/runs/<run_id>/steps/<NNN-role>/`
- Every step has isolated git worktree:
  - `<step_dir>/workspace`
- Step files:
  - `input.json`
  - `output.json`
  - `logs/stdout.txt`
  - `logs/stderr.txt`
  - `logs/events.log` (ADK event stream as JSONL)

## State Model

Authoritative state split:
- Backlog/task state: Beads (`bd`) + task `notes` (`TaskState` JSON)
- Run/step timeline: SQLite (`.norma/norma.db`)
- Human-readable artifacts: filesystem under `.norma/runs/`

`TaskState` persists:
- latest Plan/Do/Check/Act outputs
- run journal entries captured from each step's `output.progress`

## Stop Conditions

Norma stops the loop when any applies:
- budget exceeded
- dependency blocked
- verification missing/unrunnable
- replan required
- explicit terminal decision (`act.decision=close`)

Stop reason must be represented in step output (`status=stop` with concrete `stop_reason`) when applicable.

## Provider Routing Policy

Norma uses the **Agent Control Protocol (ACP)** for all agent communications. Agents are executed as ephemeral runtimes per step and wrapped with a **structured I/O layer** that ensures compliance with the role-specific JSON contracts.

- Supported standard aliases: `gemini_acp`, `opencode_acp`, `codex_acp`, `copilot_acp`, `claude_code_acp`.
- Custom agents: `generic_acp` (any binary implementing the ACP protocol).
- Execution: Each PDCA step creates a fresh agent instance, executes a single turn with mapped JSON input, and closes the runtime after validating the JSON output.

## Pool Agents (Ordered Failover)

Norma supports **pool agents** that provide ordered failover across multiple ACP agent implementations. Pool agents are useful when you want automatic fallback from a primary agent to backup agents.

### Configuration

Pool agents are configured with `type: pool` and a `pool.members` list of agent IDs to try in order:

```yaml
norma:
  agents:
    primary_agent:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
    fallback_agent:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
    my_pool:
      type: pool
      pool:
        members:
          - primary_agent
          - fallback_agent
```

### Behavior

- **Ordered sequential attempts**: Pool agents try each member in order (first to last).
- **Failover trigger**: Failover occurs only on runtime/invocation failure before a valid response is produced.
- **All-fail behavior**: If all pool members fail, the returned error includes aggregated failure details from each attempt.
- **Nested pools**: NOT allowed (MVP constraint).
- **Self-reference**: NOT allowed.

### Usage in Profiles

Pool agents can be used anywhere a regular agent is used:

```yaml
cli:
  pdca:
    plan: my_pool
    do: my_pool
    check: my_pool
    act: my_pool
  planner: my_pool
```

### Observability

When all pool members fail, the error message includes:
- Pool name
- Number of attempts
- Each member's error message in order

Example error:
```
pool "my_pool": all 2 members failed
  [1] primary_agent: create agent "primary_agent": ...
  [2] fallback_agent: create agent "fallback_agent": ...
```

## MCP Servers Configuration

Norma supports configuring **MCP (Model Context Protocol) servers** that can be referenced by agents. MCP servers provide additional tools and capabilities to agents that support them.

### Configuration

MCP servers are defined in `norma.mcp_servers`, and agents reference them by name:

```yaml
norma:
  mcp_servers:
    my_mcp_server:
      type: stdio
      cmd: ["npx", "-y", "@example/mcp-server"]
      args: ["--arg1", "value1"]
      env:
        API_KEY: ${API_KEY}
      working_dir: /path/to/workdir

  agents:
    my_agent:
      type: gemini_acp
      gemini_acp:
        model: gemini-3-flash-preview
      mcp_servers:
        - my_mcp_server
```

### Transport Types

- **stdio**: Local subprocess communication via stdin/stdout
  - Requires `cmd` (executable) and optional `args`, `env`, `working_dir`
- **http**: HTTP transport for remote MCP servers
  - Requires `url` and optional `headers`
- **sse**: Server-Sent Events transport
  - Requires `url` and optional `headers`

### Agent References

Agents can reference MCP servers in two ways:

- **Single server**: `mcp_servers: server_name` (string)
- **Multiple servers**: `mcp_servers: [server1, server2]` (array)

Pool agents automatically pass MCP server configurations to their member agents.

## References

- Canonical workflow and contracts: `AGENTS.md`
- Schemas:
  - `internal/agents/pdca/roles/plan/*.schema.json`
  - `internal/agents/pdca/roles/do/*.schema.json`
  - `internal/agents/pdca/roles/check/*.schema.json`
  - `internal/agents/pdca/roles/act/*.schema.json`

## RoleAgent Architecture

The PDCA system uses the `roleagent` package for implementation-agnostic role execution. This abstraction allows roles to be reused across different workflow types.

### Core Components

- **`roleagent.RoleContract`**: Interface that all roles must implement:
  - `Name() string` - Role identifier
  - `Schemas() SchemaPair` - Input/output JSON schemas
  - `Prompt(req AgentRequest) (string, error)` - Generate agent prompt
  - `MapRequest(req AgentRequest) (any, error)` - Transform input for role
  - `MapResponse(outBytes []byte) (AgentResponse, error)` - Parse agent output

- **`roleagent.AgentRequest`**: JSON raw message (`json.RawMessage`) containing the full input for the role

- **`roleagent.AgentResponse`**: Standardized response structure with role-specific outputs as JSON raw messages:
  - `PlanOutput`, `DoOutput`, `CheckOutput`, `ActOutput` - Role-specific data

### PDCA Composition

The PDCA roles compose the roleagent abstraction:

1. **`roles/base_role.go`**: Implements `RoleContract` with common prompt/schema handling
2. **`roles/roles.go`**: Defines role-specific `MapRequest`/`MapResponse` for plan, do, check, act
3. **`runner.go`**: Executes roles via `roleagent.Executor` with structured I/O
4. **`agent.go`**: Orchestrates the PDCA loop using the role system

### Migration Rationale

Previously, PDCA used `contracts.Role` interface with typed structs. The migration to `roleagent.RoleContract`:
- Removes PDCA-specific adapter layer
- Enables role reuse in non-PDCA workflows
- Maintains backward compatibility through JSON serialization
- Simplifies the executor integration
