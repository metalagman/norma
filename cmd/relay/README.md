# Relay User Guide

`relay` is a standalone Telegram relay server for Norma agents.

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

Non-interactive mode:

```bash
relay init --relay-root-agent gemini
```

This creates `.norma/relay.yaml`.

3. Set Telegram token:

```bash
export RELAY_TELEGRAM_TOKEN=<your_bot_token>
```

4. Start relay:

```bash
relay serve
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
- `.norma/relay.yaml`

Common keys:
- `relay.root_agent`
- `relay.telegram.token`
- `relay.workspace.mode` (`on|off|auto`)
- `relay.state_dir`

Environment overrides use `RELAY_*` (for example, `RELAY_TELEGRAM_TOKEN`).

## Troubleshooting

- `telegram token is required`:
  - set `RELAY_TELEGRAM_TOKEN`, or set `relay.telegram.token` in `.norma/relay.yaml`.
- `relay.root_agent is required`:
  - run `relay init` again (new repo) or set `relay.root_agent` to an ID from `norma.agents`.
- `agent "<id>" not available` on `/new`:
  - use one of the listed agent IDs from `/new` usage help.

## Technical Reference

- Relay technical spec: [`docs/relay.md`](../../docs/relay.md)
