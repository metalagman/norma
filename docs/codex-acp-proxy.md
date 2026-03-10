# Codex ACP Proxy

This command runs `codex mcp-server` and exposes it as an ACP agent over stdio.

Command:

```bash
norma proxy codex-acp
```

## Why this exists

- Norma ACP runners need an ACP endpoint.
- Codex CLI exposes MCP (`codex mcp-server`), not ACP directly.
- `norma proxy codex-acp` bridges MCP tools (`codex`, `codex-reply`) into ACP session calls.

## Usage

```bash
# Start proxy with defaults
norma proxy codex-acp

# Set ACP agent name seen by ACP clients in initialize.agentInfo.name
norma proxy codex-acp --name team-codex

# Configure Codex MCP `codex` tool args
norma proxy codex-acp --codex-model gpt-5.4 --codex-sandbox workspace-write

# Pass extra flags to codex mcp-server (everything after -- is forwarded)
norma proxy codex-acp -- --trace --raw
```

## Flags

- `--name`:
  ACP agent name reported in `initialize.agentInfo.name`.
  Default: `norma-codex-acp-proxy`.
- `--codex-model`:
  `model` field for MCP `codex` tool calls.
- `--codex-sandbox`:
  `sandbox` field for MCP `codex` tool calls.
  Allowed: `read-only`, `workspace-write`, `danger-full-access`.
- `--codex-approval-policy`:
  `approval-policy` field for MCP `codex` tool calls.
  Allowed: `untrusted`, `on-failure`, `on-request`, `never`.
- `--codex-profile`:
  `profile` field for MCP `codex` tool calls.
- `--codex-base-instructions`:
  `base-instructions` field for MCP `codex` tool calls.
- `--codex-developer-instructions`:
  `developer-instructions` field for MCP `codex` tool calls.
- `--codex-compact-prompt`:
  `compact-prompt` field for MCP `codex` tool calls.
- `--codex-config`:
  `config` field for MCP `codex` tool calls as a JSON object.
- `--` separator:
  All arguments after `--` are forwarded directly to `codex mcp-server`.

## Behavior

- Starts `codex mcp-server` in the current working directory.
- Verifies required MCP tools are present: `codex` and `codex-reply`.
- Opens ACP agent-side stdio connection for clients.
- For each ACP session:
  - first prompt calls MCP tool `codex` (new thread) and includes configured `--codex-*` fields
  - next prompts call MCP tool `codex-reply` (same thread), with only `threadId` + `prompt`
- Supports ACP cancellation via `session/cancel`.

## Config Note (`codex_acp` agent type)

For `type: codex_acp`, `extra_args` now target proxy arguments directly.
If you need to forward raw backend args to `codex mcp-server`, include an explicit `--` in `extra_args`.

## Exit behavior

- Returns non-zero if Codex MCP server exits unexpectedly or bridge setup fails.
- Returns zero when ACP client disconnects normally.
