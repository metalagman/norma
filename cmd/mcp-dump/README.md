# mcp-dump

`mcp-dump` inspects any stdio MCP server command and prints initialize/capability data.

## Run

```bash
mcp-dump -- <mcp-server-cmd> [args...]
```

Examples:

```bash
mcp-dump -- codex mcp-server
mcp-dump --json -- codex mcp-server
```

## Flags

- `--json`: print machine-readable JSON output.
- `--debug`: enable debug logs for the inspector.

## Output

Human-readable output includes:

- server name/version
- protocol version
- server capabilities
- tools list (with input params and response schemas)
- prompts/resources/resource templates status

## Notes

- `--` is required. Arguments before `--` are rejected.
- By default, command output is result-focused (no inspector debug/info logs).

## Repository

- Norma GitHub: <https://github.com/metalagman/norma>

## Contact

- Issues: <https://github.com/metalagman/norma/issues>
- Maintainer: [@metalagman](https://github.com/metalagman)

## License

MIT. See the repository [LICENSE](https://github.com/metalagman/norma/blob/main/LICENSE).
