# Codex ACP Bridge

This command runs `codex mcp-server` and exposes it as an ACP agent over stdio.

Command:

```bash
norma tool codex-acp
```

## Why this exists

- Norma ACP runners need an ACP endpoint.
- Codex CLI exposes MCP (`codex mcp-server`), not ACP directly.
- `norma tool codex-acp` bridges MCP tools (`codex`, `codex-reply`) into ACP session calls.

## Usage

```bash
# Start bridge with defaults
norma tool codex-acp

# Set ACP agent name seen by ACP clients in initialize.agentInfo.name
norma tool codex-acp --name team-codex

# Configure Codex MCP `codex` tool args
norma tool codex-acp --codex-model gpt-5.4 --codex-sandbox workspace-write
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

## Behavior

- Starts `codex mcp-server` in the current working directory.
- Verifies required MCP tools are present: `codex` and `codex-reply`.
- Opens ACP agent-side stdio connection for clients.
- For each ACP session:
  - first prompt calls MCP tool `codex` (new thread) and includes configured `--codex-*` fields
  - next prompts call MCP tool `codex-reply` (same thread), with only `threadId` + `prompt`
- Supports ACP cancellation via `session/cancel`.

## Config Note (`codex_acp` agent type)

For `type: codex_acp`, `extra_args` target proxy arguments directly.
Raw argument forwarding to `codex mcp-server` is not supported.

## Exit behavior

- Returns non-zero if Codex MCP server exits unexpectedly or bridge setup fails.
- Returns zero when ACP client disconnects normally.
