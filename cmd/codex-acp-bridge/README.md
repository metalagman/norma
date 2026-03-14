# codex-acp-bridge

`codex-acp-bridge` runs `codex mcp-server` and exposes it as an ACP agent over stdio.

## Run

```bash
codex-acp-bridge
```

Examples:

```bash
codex-acp-bridge
codex-acp-bridge --name team-codex
codex-acp-bridge --codex-model gpt-5.4 --codex-sandbox workspace-write
codex-acp-bridge --debug
```

## Flags

- `--name`: ACP agent name (default: `norma-codex-acp-bridge`).
- `--codex-model`: model for MCP `codex` tool calls.
- `--codex-sandbox`: sandbox for MCP `codex` tool calls (`read-only|workspace-write|danger-full-access`).
- `--codex-approval-policy`: approval policy for MCP `codex` tool calls (`untrusted|on-failure|on-request|never`).
- `--codex-profile`: profile for MCP `codex` tool calls.
- `--codex-base-instructions`: base instructions for MCP `codex` tool calls.
- `--codex-developer-instructions`: developer instructions for MCP `codex` tool calls.
- `--codex-compact-prompt`: compact prompt for MCP `codex` tool calls.
- `--codex-config`: JSON object for MCP `codex` tool `config` field.
- `--debug`: enable debug logs.

## Behavior

- Validates that Codex MCP tools `codex` and `codex-reply` are available.
- Creates separate backend Codex MCP sessions per ACP session.
- Supports ACP `session/set_model` and propagates the selected model to new Codex tool calls.
- Accepts ACP `session/set_mode` and resets the backend session, but does not currently propagate mode to Codex MCP tool arguments.

## Notes

- See also: `docs/codex-acp-bridge.md`.

## Repository

- Norma GitHub: <https://github.com/metalagman/norma>

## Contact

- Issues: <https://github.com/metalagman/norma/issues>
- Maintainer: [@metalagman](https://github.com/metalagman)

## License

MIT. See the repository [LICENSE](https://github.com/metalagman/norma/blob/main/LICENSE).
