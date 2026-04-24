# acp-repl

Chat with any ACP server from a focused terminal REPL.

`acp-repl` runs an interactive REPL against any stdio ACP server command.

## Installation

Global install (distributed via npm):

```bash
npm install -g @normahq/acp-repl@latest
```

One-off run with npx (no global install):

```bash
npx @normahq/acp-repl@latest -- <acp-server-cmd> [args...]
```

## Run

```bash
acp-repl -- <acp-server-cmd> [args...]
```

Examples:

```bash
acp-repl -- opencode acp
acp-repl --model opencode/big-pickle --mode plan -- opencode acp
acp-repl --debug -- opencode acp
```

## Flags

- `--model <id>`: call ACP `session/set_model` after session creation (unsupported servers are ignored).
- `--mode <id>`: call ACP `session/set_mode` after session creation (unsupported servers are ignored).
- `--debug`: enable debug logs.

## Interaction

- Type prompts and press Enter to run a turn.
- Type `exit` or `quit` to close the REPL.
- If ACP permission requests arrive, choose from the numbered options.

## Notes

- `--` is required. Arguments before `--` are rejected.
- Default logging is quiet for REPL lifecycle messages; use `--debug` to see them.

## Repository

- Norma GitHub: <https://github.com/normahq/norma>

## Contact

- Issues: <https://github.com/normahq/norma/issues>
- Maintainer: [@metalagman](https://github.com/metalagman)

## License

MIT. See the repository [LICENSE](https://github.com/normahq/norma/blob/master/LICENSE).
