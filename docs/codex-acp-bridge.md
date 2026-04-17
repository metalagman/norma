# Codex ACP Bridge

This command runs `codex app-server` and exposes it as an ACP agent over stdio.

Command:

```bash
norma tool codex-acp-bridge
# or when using standalone relay binary:
relay tool codex-acp-bridge
```

## Why this exists

- Norma ACP runners need an ACP endpoint.
- `codex-acp-bridge` provides a stable command name for Codex ACP integration.
- The bridge now uses Codex app-server as the backend runtime.

## Usage

```bash
# Start bridge with defaults
norma tool codex-acp-bridge

# Set ACP agent name seen by ACP clients in initialize.agentInfo.name
norma tool codex-acp-bridge --name team-codex

# Configure Codex app-server start defaults
norma tool codex-acp-bridge --codex-model gpt-5.4 --codex-sandbox workspace-write
```

## Flags

- `--name`:
  ACP agent name reported in `initialize.agentInfo.name`.
  Default: `norma-codex-acp-bridge`.
- `--codex-model`:
  App-server model override.
- `--codex-sandbox`:
  App-server sandbox mode.
  Allowed: `read-only`, `workspace-write`, `danger-full-access`.
- `--codex-approval-policy`:
  App-server approval policy.
  Allowed: `untrusted`, `on-failure`, `on-request`, `never`.
- `--codex-profile`:
  Codex config profile override.
- `--codex-base-instructions`:
  Codex base instructions override.
- `--codex-developer-instructions`:
  Codex developer instructions override.
- `--codex-compact-prompt`:
  Codex config `compact_prompt` override.
- `--codex-config`:
  Codex app-server config JSON object.

## Behavior

- Starts `codex app-server` in the current working directory.
- Opens ACP agent-side stdio connection for clients.
- Creates one backend app-server session per ACP session.
- Supports ACP cancellation via `session/cancel`.
- Supports per-session MCP servers via ACP `session/new` `mcpServers` parameter.
  - Supported transports: `stdio`, `http`.
  - `sse` is not supported.
  - Each `mcpServers[]` entry must define exactly one transport.
  - Bridge maps these values to `config.mcp_servers.<id>.*` in app-server thread start params.
- Supports `session/set_model` and `session/set_mode` for ACP session state.

## Config Note (`codex_acp` agent type)

For `type: codex_acp`, `extra_args` target bridge arguments directly.

## Exit behavior

- Returns non-zero if app-server backend setup fails.
- Returns zero when ACP client disconnects normally.
