# acp-dump

`acp-dump` inspects any stdio ACP server command and prints initialize/session details.

## Run

```bash
acp-dump -- <acp-server-cmd> [args...]
```

Examples:

```bash
acp-dump -- opencode acp
acp-dump --json -- opencode acp --debug
```

## Flags

- `--json`: print machine-readable JSON output.
- `--debug`: enable debug logs for the inspector.

## Output

Human-readable output includes:

- agent name/version
- protocol version
- ACP capabilities
- auth methods
- session id
- available session modes/models (if provided by the server)

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
