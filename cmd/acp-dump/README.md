# acp-dump

Inspect any ACP server before wiring it into your agent workflow.

`acp-dump` inspects any stdio ACP server command and prints initialize/session details.

## Installation

Global install (distributed via npm):

```bash
npm install -g @normahq/acp-dump@latest
```

One-off run with npx (no global install):

```bash
npx @normahq/acp-dump@latest -- <acp-server-cmd> [args...]
```

## Run

```bash
acp-dump -- <acp-server-cmd> [args...]
```

Examples:

```bash
acp-dump -- opencode acp
acp-dump --json -- opencode acp
acp-dump --debug -- opencode acp
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

- Norma GitHub: <https://github.com/normahq/norma>

## Contact

- Issues: <https://github.com/normahq/norma/issues>
- Maintainer: [@metalagman](https://github.com/metalagman)

## License

MIT. See the repository [LICENSE](https://github.com/normahq/norma/blob/master/LICENSE).
