# Norma Relay (V1)

`relay start` is a channel-aware background ACP service that currently binds Telegram chats/topics to ADK agents created by Norma's agent factory.

## Summary

- Runtime stack: `tgbotkit/runtime` + Google ADK runners.
- Telegram is the first supported relay channel; future channels should be added as top-level config siblings such as `relay.whatsapp`.
- Main agent: relay app key `relay.root_agent` (profile overrides via `profiles.<profile>.relay.root_agent`).
- Subagents: one session per Telegram topic (`message_thread_id`) with dedicated git worktree.
- Relay startup prompt includes workspace settings for each session; in git workspace mode it also includes session/base/current-branch context and workspace MCP guidance.
- Output streaming:
  - Thought updates: Telegram Bot API `sendMessageDraft` (plain text).
  - Final assistant response: Telegram Bot API `sendMessage` (MarkdownV2; retry without `parse_mode` on failure).
- Auth model: one-time owner authorization with startup-generated token.

## Startup Order (Required)

Relay startup order is strict:

1. Load Norma + relay config.
2. Start internal MCP lifecycle manager.
3. Start relay root agent via `agentfactory.Factory`.
4. Start Telegram runtime receiver.

Internal MCP v1 scope is config + lifecycle plumbing; server implementations can be added incrementally.

## Configuration

Relay config is loaded from one selected file (priority order):

1. Embedded defaults (`cmd/relay/relay.yaml`)
2. Runtime config in `.config/relay/config.yaml`
3. Profile app overrides in the same file (`profiles.<name>.relay.*`)
4. Environment variables (`RELAY_*`) via Viper env mapping

Relay also auto-loads a `.env` file at startup (via `godotenv`) from the relay process working directory only. Values loaded from `.env` are treated as environment variables, so `RELAY_*` entries override file config the same way as exported shell variables.

Example `.env`:

```dotenv
RELAY_TELEGRAM_TOKEN=123456:ABCDEF
RELAY_TELEGRAM_WEBHOOK_ENABLED=true
RELAY_TELEGRAM_WEBHOOK_URL=https://example.com/telegram/webhook
```

Config shape:

```yaml
norma:
  providers: {}
  mcp_servers: {}
relay: {}
profiles: {}
```

### Telegram settings

- `relay.telegram.token`: bot token (required)
- `relay.telegram.webhook.enabled`: enable local HTTP webhook endpoint (`true` => webhook mode, `false` => polling mode; default: `false`)
- `relay.telegram.webhook.url`: outgoing Telegram webhook URL (required when `relay.telegram.webhook.enabled=true`)
- `relay.telegram.webhook.auth_token`: optional webhook auth token
- `relay.telegram.webhook.listen_addr`: local webhook listen address (default: `0.0.0.0:8080`)
- `relay.telegram.webhook.path`: local webhook path (default: `/telegram/webhook`)

### Relay settings

- `relay.working_dir`: optional relay working directory (defaults to process CWD)
- `relay.state_dir`: relay state directory for persistent relay SQLite state (`relay.db`).
  - Stores owner/app KV, `relay.state` MCP KV, session metadata, and Telegram polling offset.
  - Schema is migration-versioned and auto-applied on startup.
  - Relative paths are resolved from `relay.working_dir`.
  - Default: `.config/relay`
- owner auth token is generated at runtime per `relay start` and exposed via startup `auth_url` log field
- bundled relay MCP listener always binds to local ephemeral address (`127.0.0.1:0`)
  - bundled routes on this listener:
    - `/mcp` and `/mcp/relay` for the built-in relay MCP server
- Relay config is edited via the config file itself, not through MCP.
  - relay agents should use the config path shown in the system instruction and edit `.config/relay/config.yaml` directly
- `relay.mcp_servers`: extra MCP server IDs for all relay-started sessions (resolved from `norma.mcp_servers`)
  - effective MCP IDs = bundled defaults + `norma.providers.<agent>.mcp_servers` + `relay.mcp_servers` (deduplicated)
- `relay.system_instructions`: optional relay instruction text applied to all sessions
  - value: instruction text appended in relay prompt for all agents
  - effective relay instruction order: built-in relay instructions + `norma.providers.<agent>.system_instructions` + `relay.system_instructions` (last wins by position)
  - `relay init` generates a channel-aware example prompt
- `relay.workspace.mode`: `on|off|auto` (default `auto`)
  - `on`: always use Git worktrees per session; startup fails if `working_dir` is not a Git repository
  - `off`: run agents directly in relay `working_dir` (no `relay.workspace` namespace)
  - `auto`: enable worktrees only when `working_dir` is a Git repo, otherwise fallback to `off`
- `relay.workspace.base_branch`: base branch used for workspace sync/export (for example `main`, `master`, `develop`)
  - `relay init` detects current HEAD branch and writes it when available
  - if empty, relay resolves base branch from current HEAD at startup
  - `relay.workspace.export` requires main repo to be on this branch
- Relay is Beads-independent by default and does not auto-start bundled `norma.tasks` MCP.

## Session Model

Session key:

- Root relay session: `(chat_id, topic_id=0)`
- Topic subagent session: `(chat_id, topic_id)`
- Canonical relay session IDs are channel-scoped. Telegram uses `tg-<chat_id>-<topic_id>`.
- Root sessions are created lazily on the first owner message in that chat (`topic_id=0`).

Session runtimes are still in-memory, but metadata is persisted in `relay.db`.
Relay lazy-restores a topic session on first message after restart when metadata exists.

## Message Flow

1. User sends Telegram message.
2. Relay resolves session by `(chat_id, topic_id)`.
3. If topic session is missing in memory, relay attempts lazy restore from persisted metadata.
4. Relay calls ADK runner for that session.
5. Relay streams partial updates to Telegram using Bot API `sendMessageDraft`.

## Telegram Messaging Behavior

Per model turn:

1. Thought events are emitted as plain `sendMessageDraft` updates using a stable `draft_id`; relay also sends `sendChatAction` with `typing` for the same chat/topic.
2. Final assistant text is sent with `sendMessage` using MarkdownV2.
3. If MarkdownV2 delivery fails, relay retries once without `parse_mode`.

## Subagent Spawn

Two v1 spawn paths are supported:

1. Manual: `/new <agent_name>`
2. Agent/tool path: relay MCP `relay.agents.start`

Both paths create:

- A new Telegram forum topic
- A topic-bound ADK session
- A dedicated Git worktree when `relay.workspace.mode` resolves to enabled

### Manual session control

- `/new <agent_name>` creates a new topic-bound subagent session.
- `/close` closes the current topic (when `message_thread_id > 0`) and stops the current agent session.
- In the main chat (`topic_id = 0`), `/close` only stops the root session (topic is not closed).

## Relay MCP API (V1)

- built-in server ID: `relay`
- `relay.agents.start`
  - input: `agent_name`
  - optional input: `locator`
    - relay-mounted agents should omit `locator` so the current caller session context is used automatically
    - external callers can provide `locator.channel_type` plus `locator.address`
    - Telegram locator example: `{"channel_type":"telegram","address":{"chat_id":123456}}`
  - output: structured session object including `channel_type`, `address_key`, `session_id`, `chat_id`, `topic_id`, `agent_name`, `description`, `mcp_servers`
- `relay.agents.stop`
  - input: `session_id`
- `relay.agents.list`
  - output: structured `agents[]` entries, including persisted sessions with `status=persisted`
- `relay.agents.get`
  - input: `session_id`
  - output: structured `agent` object
- Deprecated aliases (still supported): `relay.agents.stop_agent`, `relay.agents.list_agents`, `relay.agents.get_agent`

Relay agents should prefer `relay.agents.start` without a locator when they spawn subagents from the current chat context.

## Workspace MCP Usage

- `relay.workspace.import`
  - rebases the session workspace onto the configured base branch
  - works for active or persisted sessions as long as workspace metadata exists in `relay.db`
- `relay.workspace.export`
  - squash-merges the session workspace branch into the configured base branch with the provided Conventional Commit message
  - also works for persisted sessions before lazy restore

## Acceptance/Verification Scenarios

1. Startup order enforces internal MCP -> relay agent -> bot runtime.
2. Polling mode starts by default when `relay.telegram.webhook.enabled=false`.
3. Webhook mode (`relay.telegram.webhook.enabled=true`) fails fast without `relay.telegram.webhook.url`.
4. `/start <token>` registers owner once; non-owner traffic is rejected.
5. `/new <agent>` creates topic + relay session and persists session metadata.
6. Relay MCP `start` creates topic + session and returns IDs.
7. Restart clears in-memory sessions but topic sessions are lazy-restored from persisted metadata.
8. Polling mode resumes from persisted Telegram offset in relay state DB.
9. Partial thought updates are sent with Telegram Bot API `sendMessageDraft`.
10. Final assistant response is sent with `sendMessage` using MarkdownV2 with fallback retry without `parse_mode`.
13. `/close` in a topic closes that topic and stops the session; `/close` in root chat stops only root session.
