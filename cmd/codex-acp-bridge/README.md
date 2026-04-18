# codex-acp-bridge

`codex-acp-bridge` runs the Codex bridge backend and exposes it as an ACP agent over stdio.

## Installation

Global install (distributed via npm):

```bash
npm install -g @normahq/codex-acp-bridge@latest
```

One-off run with npx (no global install):

```bash
npx @normahq/codex-acp-bridge@latest
```

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

- `--name`: ACP agent name override (default: `norma-codex-acp-bridge`).
- `--codex-model`: backend model override.
- `--codex-sandbox`: backend sandbox override (`read-only|workspace-write|danger-full-access`).
- `--codex-approval-policy`: backend approval policy override (`untrusted|on-failure|on-request|never`).
- `--codex-profile`: codex config profile override.
- `--codex-base-instructions`: codex base instructions override.
- `--codex-developer-instructions`: codex developer instructions override.
- `--codex-compact-prompt`: codex config `compact_prompt` override.
- `--codex-config`: codex backend config JSON object.
- `--debug`: enable debug logs.

## Behavior

- Creates separate backend sessions per ACP session.
- Working directory precedence for each backend session:
  - Uses ACP `session/new.params.cwd` when provided.
  - Falls back to the bridge process working directory when `cwd` is not provided.
- Supports ACP `session/set_model` and `session/set_mode`.
  - `session/set_model` is applied to subsequent `turn/start` payloads.
  - `session/set_mode` is currently stored as ACP-side session state and is not forwarded into backend request payloads.
- Supports per-session MCP servers via ACP `session/new` `mcpServers` parameter (stdio and http transports only; `sse` is rejected).
- Preserves bridge command identity while using backend runtime semantics.

## MCP Servers

The bridge supports passing MCP servers to Codex via ACP `session/new`. On thread start, any MCP servers provided in `mcpServers` are translated into Codex config values under `config.mcp_servers`.

Example ACP `session/new` request with MCP servers:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/new",
  "params": {
    "cwd": "/workspace",
    "mcpServers": [
      {
        "stdio": {
          "name": "filesystem",
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
        }
      },
      {
        "http": {
          "name": "github",
          "url": "https://api.github.com"
        }
      }
    ]
  }
}
```

Supported transports: `stdio`, `http`. The `sse` transport is explicitly rejected.

## Notes

- See also: `docs/codex-acp-bridge.md`.

## Repository

- Norma GitHub: <https://github.com/normahq/norma>

## Contact

- Issues: <https://github.com/normahq/norma/issues>
- Maintainer: [@metalagman](https://github.com/metalagman)

## License

MIT. See the repository [LICENSE](https://github.com/normahq/norma/blob/master/LICENSE).
