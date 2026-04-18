# Codex ACP (Bridge Backend) JSON API Spec

Status: draft  
Date: 2026-04-12  
Audience: `codex-acp-bridge` adapter maintainers

## Source of truth

This document is code-first for Norma behavior:
- backend schema shape comes from `codex backend` JSON schema output.
- adapter projection semantics come from `internal/apps/codexacpbridge` implementation and tests.

```bash
codex backend generate-json-schema --experimental --out /tmp/codex-app-schema-docspec
```

Primary files:
- `/tmp/codex-app-schema-docspec/ServerNotification.json`
- `/tmp/codex-app-schema-docspec/ServerRequest.json`
- `/tmp/codex-app-schema-docspec/*Response.json`

This document does not require backend source internals; it documents observable backend schema plus current Norma adapter behavior.

## API surface summary

- Server notifications: 51 methods
- Server-initiated requests: 9 methods
- `ThreadItem` variants: 16 types
- `ThreadStatus.activeFlags`: `waitingOnApproval`, `waitingOnUserInput`

## Required adapter invariants

- Correlate every streaming/update event by `(threadId, turnId, itemId)` when present.
- Correlate every server request lifecycle by `(threadId, requestId)`.
- Preserve streaming text exactly as received.
- Preserve `item/*/outputDelta` text exactly as received when forwarding ACP tool call updates.
- Do not trim, collapse, or normalize whitespace in message deltas or completed text.
- Treat `item/completed` payload as authoritative final state for that item.

## ACP projection (normative)

The adapter should project Codex backend events into ACP session semantics as follows.

### Core notification mapping

| Codex method | Required params | ACP projection | Notes |
| --- | --- | --- | --- |
| `thread/started` | `thread` | session/thread started metadata | Use `thread.id` as backend thread handle. |
| `thread/status/changed` | `threadId,status` | status update | Forward `status.type`; include `activeFlags` if present. |
| `turn/started` | `threadId,turn` | turn started | Keep `turn.id` for correlation. |
| `item/started` | `threadId,turnId,item` | item lifecycle started | Branch on `item.type`. |
| `item/agentMessage/delta` | `threadId,turnId,itemId,delta` | `session/update.agent_message_chunk` | Append delta byte-for-byte. |
| `item/plan/delta` | `threadId,turnId,itemId,delta` | `session/update.agent_thought_chunk` or plan channel | Mark as experimental. |
| `item/reasoning/textDelta` | `threadId,turnId,itemId,contentIndex,delta` | `session/update.agent_thought_chunk` | Preserve index ordering. |
| `item/reasoning/summaryPartAdded` | `threadId,turnId,itemId,summaryIndex` | `session/update.agent_thought_chunk` | Emits a summary progress thought string (no separate summary bucket state). |
| `item/reasoning/summaryTextDelta` | `threadId,turnId,itemId,summaryIndex,delta` | `session/update.agent_thought_chunk` | Append by summary index. |
| `item/commandExecution/outputDelta` | `threadId,turnId,itemId,delta` | `session/update.tool_call_update` | Text output chunk for command item. |
| `item/fileChange/outputDelta` | `threadId,turnId,itemId,delta` | `session/update.tool_call_update` | Streaming patch preview/update. |
| `item/mcpToolCall/progress` | `threadId,turnId,itemId,message` | `session/update.tool_call_update` | Progress text. |
| `item/completed` | `threadId,turnId,item` | item lifecycle completed | Finalize item state by `item.type`. |
| `turn/plan/updated` | `threadId,turnId,plan` | plan snapshot update | Snapshot; do not assume continuity with deltas. |
| `turn/diff/updated` | `threadId,turnId,diff` | diff snapshot update | Unified diff at turn scope. |
| `thread/tokenUsage/updated` | `threadId,turnId,tokenUsage` | usage update | Forwards `tokenUsage.last.{inputTokens,outputTokens,totalTokens,cachedInputTokens}` into ACP meta usage (`cachedInputTokens` -> `cachedReadTokens`). |
| `error` | `threadId,turnId,error,willRetry` | error event | If `willRetry=false`, finalize turn as failed/interrupted. |
| `turn/completed` | `threadId,turn` | turn completed | Terminal per-turn signal. |
| `serverRequest/resolved` | `threadId,requestId` | request lifecycle ack | Clear pending request state. |

### Command output distinction

`command/exec/outputDelta` and `item/commandExecution/outputDelta` are different channels and must not be merged blindly.

- `command/exec/outputDelta`: connection-scoped raw process stream with `deltaBase64`, `processId`, `stream`, `capReached`.
  - Current adapter behavior: emits an ACP thought summary containing process/stream/size/cap metadata; it does not forward raw decoded bytes into ACP content fields.
- `item/commandExecution/outputDelta`: turn/item-scoped text stream keyed by `itemId`.

### Server request mapping

Each server request must be handled and answered with the correct response shape, then tracked until `serverRequest/resolved`.

| Request method | Required params | Response contract |
| --- | --- | --- |
| `item/commandExecution/requestApproval` | `itemId,threadId,turnId` | `CommandExecutionRequestApprovalResponse` (`decision`) |
| `item/fileChange/requestApproval` | `itemId,threadId,turnId` | `FileChangeRequestApprovalResponse` (`decision`) |
| `item/permissions/requestApproval` | `itemId,permissions,threadId,turnId` | `PermissionsRequestApprovalResponse` (`permissions`, optional `scope`) |
| `item/tool/call` | `arguments,callId,threadId,tool,turnId` | `DynamicToolCallResponse` (`success`,`contentItems`) |
| `item/tool/requestUserInput` | `itemId,questions,threadId,turnId` | `ToolRequestUserInputResponse` (`answers`) |
| `mcpServer/elicitation/request` | `serverName,threadId` (+ mode-specific fields) | `McpServerElicitationRequestResponse` (`action`, optional `content`) |
| `account/chatgptAuthTokens/refresh` | `reason` | `ChatgptAuthTokensRefreshResponse` (`accessToken`,`chatgptAccountId`) |
| `applyPatchApproval` (deprecated) | `callId,conversationId,fileChanges` | `ApplyPatchApprovalResponse` (`decision`) |
| `execCommandApproval` (deprecated) | `callId,command,conversationId,cwd,parsedCmd` | `ExecCommandApprovalResponse` (`decision`) |

Decision values used by approval responses include:
- `accept`
- `acceptForSession`
- `decline`
- `cancel`

### Partial forwarding behavior (implemented)

For request types without a native ACP equivalent, the adapter uses ACP `session/request_permission` to collect a user decision, then returns schema-valid backend responses:

- `item/tool/call`: returns `DynamicToolCallResponse` with `success=false` and explanatory `contentItems`.
- `item/tool/requestUserInput`: maps chosen option labels into `answers[question_id].answers[]`.
- `mcpServer/elicitation/request`: maps decision to `action=accept|decline|cancel` and includes `_meta` passthrough when provided.
- `applyPatchApproval` / `execCommandApproval`: ACP approval outcomes are translated to legacy decisions:
  `accept -> approved`, `acceptForSession -> approved_for_session`, `decline -> denied`, `cancel -> abort`.
- `account/chatgptAuthTokens/refresh`: adapter responds from environment when available:
  `CODEX_CHATGPT_ACCESS_TOKEN`, `CODEX_CHATGPT_ACCOUNT_ID`, optional `CODEX_CHATGPT_PLAN_TYPE`;
  otherwise returns a structured request error.

## Coverage tiers (recommended)

### Tier 1 (must map now)

- `thread/started`
- `thread/status/changed`
- `turn/started`
- `item/started`
- `item/agentMessage/delta`
- `item/completed`
- `turn/completed`
- `error`
- `serverRequest/resolved`
- `item/commandExecution/requestApproval`
- `item/fileChange/requestApproval`
- `item/permissions/requestApproval`
- `item/tool/call`
- `item/tool/requestUserInput`
- `mcpServer/elicitation/request`
- `thread/tokenUsage/updated`

### Tier 2 (should map)

- `item/plan/delta`
- `item/reasoning/textDelta`
- `item/reasoning/summaryPartAdded`
- `item/reasoning/summaryTextDelta`
- `item/commandExecution/outputDelta`
- `item/commandExecution/terminalInteraction`
- `item/fileChange/outputDelta`
- `item/mcpToolCall/progress`
- `item/autoApprovalReview/started`
- `item/autoApprovalReview/completed`
- `turn/plan/updated`
- `turn/diff/updated`
- `model/rerouted`
- `thread/compacted`
- `mcpServer/startupStatus/updated`
- `mcpServer/oauthLogin/completed`
- `account/rateLimits/updated`
- `account/updated`
- `account/login/completed`
- `skills/changed`
- `app/list/updated`

### Tier 3 (optional/experimental)

- `thread/realtime/started`
- `thread/realtime/itemAdded`
- `thread/realtime/transcriptUpdated`
- `thread/realtime/outputAudio/delta`
- `thread/realtime/error`
- `thread/realtime/closed`
- `fuzzyFileSearch/sessionUpdated`
- `fuzzyFileSearch/sessionCompleted`
- `fs/changed`
- `command/exec/outputDelta`
- `hook/started`
- `hook/completed`
- `deprecationNotice`
- `configWarning`
- `windows/worldWritableWarning`
- `windowsSandbox/setupCompleted`
- `thread/archived`
- `thread/unarchived`
- `thread/closed`
- `thread/name/updated`
- `account/chatgptAuthTokens/refresh`
- `applyPatchApproval`
- `execCommandApproval`

## Full method inventory

### Notifications (51)

```text
account/login/completed
account/rateLimits/updated
account/updated
app/list/updated
command/exec/outputDelta
configWarning
deprecationNotice
error
fs/changed
fuzzyFileSearch/sessionCompleted
fuzzyFileSearch/sessionUpdated
hook/completed
hook/started
item/agentMessage/delta
item/autoApprovalReview/completed
item/autoApprovalReview/started
item/commandExecution/outputDelta
item/commandExecution/terminalInteraction
item/completed
item/fileChange/outputDelta
item/mcpToolCall/progress
item/plan/delta
item/reasoning/summaryPartAdded
item/reasoning/summaryTextDelta
item/reasoning/textDelta
item/started
mcpServer/oauthLogin/completed
mcpServer/startupStatus/updated
model/rerouted
serverRequest/resolved
skills/changed
thread/archived
thread/closed
thread/compacted
thread/name/updated
thread/realtime/closed
thread/realtime/error
thread/realtime/itemAdded
thread/realtime/outputAudio/delta
thread/realtime/started
thread/realtime/transcriptUpdated
thread/started
thread/status/changed
thread/tokenUsage/updated
thread/unarchived
turn/completed
turn/diff/updated
turn/plan/updated
turn/started
windows/worldWritableWarning
windowsSandbox/setupCompleted
```

### Server requests (9)

```text
account/chatgptAuthTokens/refresh
applyPatchApproval
execCommandApproval
item/commandExecution/requestApproval
item/fileChange/requestApproval
item/permissions/requestApproval
item/tool/call
item/tool/requestUserInput
mcpServer/elicitation/request
```

## Data-model notes for adapter state

- `ThreadStatus.type` enum: `notLoaded | idle | systemError | active`.
- `ThreadStatus.active.activeFlags` enum: `waitingOnApproval | waitingOnUserInput`.
- `Turn.status` enum: `completed | interrupted | failed | inProgress`.
- `ThreadItem.type` enum:
  `userMessage | hookPrompt | agentMessage | plan | reasoning | commandExecution | fileChange | mcpToolCall | dynamicToolCall | collabAgentToolCall | webSearch | imageView | imageGeneration | enteredReviewMode | exitedReviewMode | contextCompaction`.

## Known pitfalls to avoid

- Do not infer end-of-turn from last message chunk; wait for `turn/completed`.
- Do not treat plan deltas as authoritative final plan text.
- Do not mix connection-scoped `command/exec/outputDelta` with turn-scoped command item output.
- Do not drop or rewrite whitespace in message or reasoning deltas.
- Do not consider a server request done before `serverRequest/resolved`.

## Implementation status (current)

- `internal/apps/codexacpbridge` currently maps all methods listed in this schema inventory (`51` notifications, `9` server requests).
