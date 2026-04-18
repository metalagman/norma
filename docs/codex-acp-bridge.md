# Codex ACP Bridge

This command runs the Codex bridge backend and exposes it as an ACP agent over stdio.

Command:

```bash
norma tool codex-acp-bridge
# or when using standalone relay binary:
relay tool codex-acp-bridge
# standalone compatibility binary:
codex-acp-bridge
```

## Why this exists

- Norma ACP runners need an ACP endpoint.
- `codex-acp-bridge` provides a stable command name for Codex ACP integration.
- The bridge uses the Codex backend runtime.

## Usage

```bash
# Start bridge with defaults
norma tool codex-acp-bridge

# Set ACP agent name seen by ACP clients in initialize.agentInfo.name
norma tool codex-acp-bridge --name team-codex

# Configure Codex backend start defaults
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
  Codex backend config JSON object.

## Behavior

- Starts the Codex backend with per-session working directory selection:
  - If ACP `session/new.params.cwd` is set, that value is used for the backend process.
  - Otherwise, the bridge process working directory is used.
- Opens ACP agent-side stdio connection for clients.
- Creates one backend session per ACP session.
- Supports ACP cancellation via `session/cancel`.
- Supports per-session MCP servers via ACP `session/new` `mcpServers` parameter.
  - Supported transports: `stdio`, `http`.
  - `sse` is not supported.
  - Each `mcpServers[]` entry must define exactly one transport.
  - Bridge maps these values to `config.mcp_servers.<id>.*` in backend thread start params.
- Supports `session/set_model` and `session/set_mode` for ACP session state.
  - `session/set_model` updates model selection used by subsequent `turn/start` calls.
  - `session/set_mode` is stored in ACP session state only; current bridge implementation does not forward mode into backend `thread/start` or `turn/start` payload fields.

## Config Note (`codex_acp` agent type)

For `type: codex_acp`, `extra_args` target bridge arguments directly.

## Exit behavior

- Returns non-zero if backend setup fails.
- Returns zero when ACP client disconnects normally.
