# Relay User Guide

`relay` is a channel-aware background ACP service for Norma agents. Telegram is the first supported channel.

## Quickstart

1. Install binaries:

```bash
go install github.com/normahq/norma/cmd/norma@latest
go install github.com/normahq/norma/cmd/relay@latest
```

2. Generate relay config:

```bash
relay init
```

This creates `.config/relay/config.yaml` and prompts for:
- `relay.root_agent` (required, auto-detected agents only; default by priority: codex, opencode, copilot, gemini, claude_code)
- `relay.telegram.token` (optional, press Enter to skip)

`relay init` also generates an example prompt at `relay.system_instructions` so you can customize relay behavior quickly.
That example now includes guidance for session-scoped relay MCP usage and workspace MCP import/export.
Built-in relay MCP is exposed as one bundled server ID, `relay`, with tool namespaces under `relay.*`.
Relay config itself is not exposed via MCP; relay agents should edit `.config/relay/config.yaml` directly when needed.

3. Set or override Telegram token with env:

```bash
export RELAY_TELEGRAM_TOKEN=<your_bot_token>
```

You can also put env overrides in `.env` (auto-loaded by `relay` from the current working directory):

```dotenv
RELAY_TELEGRAM_TOKEN=<your_bot_token>
```

4. Start relay:

```bash
relay start
```

5. Authenticate as owner:
- Open the bot link from startup logs (`https://t.me/<bot>?start=<token>`), or send `/start <token>` manually in private chat.

6. Start subagent sessions:

```text
/new <agent_id>
```

To stop:
- `/close` in a topic: closes topic and stops that topic session.
- `/close` in root chat: stops only the root session.

## Config

Primary relay config file:
- `.config/relay/config.yaml`

Common keys:
- `norma.agents`
- `norma.mcp_servers`
- `relay.root_agent`
- `relay.telegram.token`
- future channels are expected as top-level siblings such as `relay.whatsapp`
- `relay.mcp_servers` (extra MCP server IDs for all relay-started sessions, from `norma.mcp_servers`)
- `relay.system_instructions` (relay-only instruction override; applied to all sessions, appended after `norma.agents.<id>.system_instructions`)
- `relay.workspace.mode` (`on|off|auto`)
- `relay.workspace.base_branch` (base branch for workspace import/export; auto-detected by `relay init` when possible)
- `relay.state_dir` (default `.config/relay`; stores migration-managed `relay.db`)

Environment overrides use `RELAY_*` (for example, `RELAY_TELEGRAM_TOKEN`). `.env` is auto-loaded on startup from the current working directory only (for example `<cwd>/.env`).

## Troubleshooting

- `telegram token is required`:
  - set `RELAY_TELEGRAM_TOKEN`, or set `relay.telegram.token` in `.config/relay/config.yaml`.
- `relay.root_agent is required`:
  - run `relay init` again (new repo) or set `relay.root_agent` to an ID from `norma.agents`.
- `agent "<id>" not available` on `/new`:
  - use one of the listed agent IDs from `/new` usage help.

## Technical Reference

- Relay technical spec: [`docs/relay.md`](../../docs/relay.md)
