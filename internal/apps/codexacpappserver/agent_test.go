package codexacp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

func TestBuildThreadStartParamsIncludesConfigAndMCPServers(t *testing.T) {
	params := buildThreadStartParams(
		"/tmp/work",
		codexAppConfig{
			ApprovalPolicy:   "on-request",
			BaseInstructions: "base",
			CompactPrompt:    "compact",
			Config: map[string]any{
				"foo": "bar",
			},
			DeveloperInstructions: "dev",
			Model:                 "gpt-5.4",
			Profile:               "dev-profile",
			Sandbox:               "workspace-write",
		},
		"",
		map[string]acp.McpServer{
			"docs": {
				Stdio: &acp.McpServerStdio{
					Name:    "docs",
					Command: "docs-server",
					Args:    []string{"--listen"},
				},
			},
		},
	)

	if got := stringValue(params, "cwd"); got != "/tmp/work" {
		t.Fatalf("cwd = %q, want %q", got, "/tmp/work")
	}
	if got := stringValue(params, "model"); got != "gpt-5.4" {
		t.Fatalf("model = %q, want %q", got, "gpt-5.4")
	}
	if got := stringValue(params, "approvalPolicy"); got != "on-request" {
		t.Fatalf("approvalPolicy = %q, want %q", got, "on-request")
	}
	config := mapValue(params, "config")
	if got := stringValue(config, "profile"); got != "dev-profile" {
		t.Fatalf("config.profile = %q, want %q", got, "dev-profile")
	}
	if got := stringValue(config, "compact_prompt"); got != "compact" {
		t.Fatalf("config.compact_prompt = %q, want %q", got, "compact")
	}
	if _, ok := config["mcp_servers"]; !ok {
		t.Fatalf("config.mcp_servers missing")
	}
}

func TestResolveAgentIdentityFromUserAgent(t *testing.T) {
	name, version := resolveAgentIdentity("", parseAppServerIdentity("codex_vscode/0.1.0 (darwin)"))
	if name != "codex_vscode" {
		t.Fatalf("name = %q, want %q", name, "codex_vscode")
	}
	if version != "0.1.0" {
		t.Fatalf("version = %q, want %q", version, "0.1.0")
	}
}

func TestPromptStreamsAppServerNotificationsToACPUpdates(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "commandExecution",
			"id":      "item-cmd-1",
			"status":  "inProgress",
			"command": "go test ./...",
		},
	})
	queueNotification(session, "item/commandExecution/outputDelta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-cmd-1",
		"delta":    "  ok   ./...",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":   "commandExecution",
			"id":     "item-cmd-1",
			"status": "completed",
		},
	})
	queueNotification(session, "turn/plan/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"plan": []any{
			map[string]any{"step": "Run tests", "status": "completed"},
		},
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "Hi",
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    " ",
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "done",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if len(updates) == 0 {
		t.Fatal("expected ACP session updates")
	}
	if !containsAgentMessageText(updates, "done") {
		t.Fatalf("missing agent message delta in ACP updates: %#v", updates)
	}
	if !containsAgentMessageText(updates, " ") {
		t.Fatalf("missing whitespace-only agent message delta in ACP updates: %#v", updates)
	}
	if !containsToolCallText(updates, "  ok   ./...") {
		t.Fatalf("missing tool call output delta with leading spaces in ACP updates: %#v", updates)
	}
	if !containsPlanEntry(updates, "Run tests") {
		t.Fatalf("missing plan update in ACP updates: %#v", updates)
	}
	if !containsToolCall(updates, "codex-item-item-cmd-1") {
		t.Fatalf("missing tool call start/update in ACP updates: %#v", updates)
	}
}

func TestPromptBridgesCommandApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/commandExecution/requestApproval", json.RawMessage("1"), map[string]any{
		"threadId":           "thr-1",
		"turnId":             "turn-1",
		"itemId":             "item-cmd-1",
		"command":            "curl example.com",
		"availableDecisions": []any{decisionDecline, decisionAcceptForSession},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-2"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	if got := len(conn.permissionRequests); got != 1 {
		t.Fatalf("permission requests = %d, want 1", got)
	}
	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	decision := stringValue(responses[0], "decision")
	if decision != decisionAcceptForSession {
		t.Fatalf("approval decision = %q, want %q", decision, decisionAcceptForSession)
	}
}

func TestPromptFallbackRespondsUnsupportedServerRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "custom/unknown", json.RawMessage("2"), map[string]any{"foo": "bar"})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	errResponses := session.errorResponsesSnapshot()
	if len(errResponses) != 1 {
		t.Fatalf("error responses = %d, want 1", len(errResponses))
	}
	if got := errResponses[0]["message"]; got != "unsupported server request" {
		t.Fatalf("fallback error message = %v, want %q", got, "unsupported server request")
	}
}

func TestPromptMapsExtendedNotifications(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "thread/started", map[string]any{
		"thread": map[string]any{
			"id": "thr-1",
		},
	})
	queueNotification(session, "turn/started", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "inProgress",
		},
	})
	queueNotification(session, "thread/status/changed", map[string]any{
		"threadId": "thr-1",
		"status": map[string]any{
			"type":        "active",
			"activeFlags": []any{"waitingOnApproval"},
		},
	})
	// Duplicate status should be deduplicated.
	queueNotification(session, "thread/status/changed", map[string]any{
		"threadId": "thr-1",
		"status": map[string]any{
			"type":        "active",
			"activeFlags": []any{"waitingOnApproval"},
		},
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "Run ",
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "tests",
	})
	queueNotification(session, "item/reasoning/summaryPartAdded", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reason-1",
		"summaryIndex": 2,
	})
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "commandExecution",
			"id":      "item-cmd-1",
			"status":  "inProgress",
			"command": "echo hi",
		},
	})
	queueNotification(session, "item/commandExecution/terminalInteraction", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"itemId":    "item-cmd-1",
		"processId": "p-1",
		"stdin":     "y\n",
	})
	queueNotification(session, "item/autoApprovalReview/started", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"targetItemId": "item-cmd-1",
		"review": map[string]any{
			"status":    "inProgress",
			"riskLevel": "highRiskCyberActivity",
			"rationale": "command touches network",
		},
	})
	queueNotification(session, "item/autoApprovalReview/completed", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"targetItemId": "item-cmd-1",
		"review": map[string]any{
			"status":    "approved",
			"riskLevel": "highRiskCyberActivity",
		},
	})
	queueNotification(session, "hook/started", map[string]any{
		"threadId": "thr-1",
		"run": map[string]any{
			"id":        "hk-1",
			"eventName": "preCommand",
			"status":    "running",
		},
	})
	queueNotification(session, "hook/completed", map[string]any{
		"threadId": "thr-1",
		"run": map[string]any{
			"id":            "hk-1",
			"eventName":     "preCommand",
			"status":        "completed",
			"statusMessage": "ok",
		},
	})
	queueNotification(session, "mcpServer/startupStatus/updated", map[string]any{
		"name":   "docs",
		"status": "failed",
		"error":  "spawn failed",
	})
	queueNotification(session, "mcpServer/oauthLogin/completed", map[string]any{
		"name":    "docs",
		"success": false,
		"error":   "device code expired",
	})
	queueNotification(session, "model/rerouted", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"fromModel": "gpt-5.4",
		"toModel":   "gpt-5.4-mini",
		"reason":    "highRiskCyberActivity",
	})
	queueNotification(session, "configWarning", map[string]any{
		"summary": "Invalid config value",
		"path":    "/tmp/config.toml",
		"details": "using default",
	})
	queueNotification(session, "deprecationNotice", map[string]any{
		"summary": "old flag is deprecated",
		"details": "use --new-flag",
	})
	queueNotification(session, "account/login/completed", map[string]any{
		"accountId": "acc-1",
	})
	queueNotification(session, "account/updated", map[string]any{})
	queueNotification(session, "app/list/updated", map[string]any{})
	queueNotification(session, "skills/changed", map[string]any{})
	queueNotification(session, "thread/compacted", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/archived", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/unarchived", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/closed", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/name/updated", map[string]any{
		"threadId":   "thr-1",
		"threadName": "ACP Mapping Thread",
	})
	queueNotification(session, "windows/worldWritableWarning", map[string]any{
		"extraCount": 2,
		"failedScan": false,
		"samplePaths": []any{
			"C:/tmp/a",
			"C:/tmp/b",
		},
	})
	queueNotification(session, "windowsSandbox/setupCompleted", map[string]any{
		"mode":    "sandbox",
		"success": true,
		"error":   nil,
	})
	queueNotification(session, "thread/realtime/started", map[string]any{
		"threadId":  "thr-1",
		"version":   "v1",
		"sessionId": "rt-1",
	})
	queueNotification(session, "thread/realtime/itemAdded", map[string]any{
		"threadId": "thr-1",
		"item": map[string]any{
			"type": "message",
			"id":   "rt-item-1",
		},
	})
	queueNotification(session, "thread/realtime/outputAudio/delta", map[string]any{
		"threadId": "thr-1",
		"audio": map[string]any{
			"itemId":      "rt-item-1",
			"sampleRate":  24000,
			"numChannels": 1,
			"data":        "AQID",
		},
	})
	queueNotification(session, "thread/realtime/transcriptUpdated", map[string]any{
		"threadId": "thr-1",
		"role":     "assistant",
		"text":     "hello realtime",
	})
	queueNotification(session, "thread/realtime/error", map[string]any{
		"threadId": "thr-1",
		"message":  "transport issue",
	})
	queueNotification(session, "thread/realtime/closed", map[string]any{
		"threadId": "thr-1",
		"reason":   "done",
	})
	queueNotification(session, "fs/changed", map[string]any{
		"watchId": "watch-1",
		"changedPaths": []any{
			"/tmp/a.go",
			"/tmp/b.go",
		},
	})
	queueNotification(session, "fuzzyFileSearch/sessionUpdated", map[string]any{
		"sessionId": "fuzzy-1",
		"query":     "agent",
		"files": []any{
			map[string]any{"path": "internal/apps/codexacpappserver/agent.go", "score": 0.9},
		},
	})
	queueNotification(session, "fuzzyFileSearch/sessionCompleted", map[string]any{
		"sessionId": "fuzzy-1",
	})
	queueNotification(session, "command/exec/outputDelta", map[string]any{
		"processId":   "proc-1",
		"stream":      "stdout",
		"deltaBase64": "AQID",
		"capReached":  false,
	})
	queueNotification(session, "turn/diff/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"diff":     "@@ -1 +1 @@\n-foo\n+bar\n  tail",
	})
	queueNotification(session, "account/rateLimits/updated", map[string]any{
		"rateLimits": map[string]any{
			"planType": "plus",
			"primary": map[string]any{
				"usedPercent": 12,
			},
		},
	})
	queueNotification(session, "thread/tokenUsage/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"inputTokens":       10,
				"outputTokens":      2,
				"totalTokens":       12,
				"cachedInputTokens": 4,
			},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if !containsPlanEntry(updates, "Run tests") {
		t.Fatalf("missing aggregated plan delta update: %#v", updates)
	}
	if countThoughtText(updates, "Thread status: active (waitingOnApproval)") != 1 {
		t.Fatalf("expected deduplicated thread status thought, updates: %#v", updates)
	}
	if !containsToolCallText(updates, "y\n") {
		t.Fatalf("missing terminal interaction content: %#v", updates)
	}
	if !containsToolCall(updates, "codex-guardian-item-cmd-1") {
		t.Fatalf("missing guardian synthetic tool call updates: %#v", updates)
	}
	if !containsToolCall(updates, "codex-hook-hk-1") {
		t.Fatalf("missing hook synthetic tool call updates: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Model rerouted:") {
		t.Fatalf("missing model rerouted thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Config warning:") {
		t.Fatalf("missing config warning thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Deprecation notice:") {
		t.Fatalf("missing deprecation notice thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Account login completed: acc-1.") {
		t.Fatalf("missing account login completed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Account updated.") {
		t.Fatalf("missing account updated thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "App list updated.") {
		t.Fatalf("missing app list updated thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Skills changed.") {
		t.Fatalf("missing skills changed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Thread compacted.") {
		t.Fatalf("missing thread compacted thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Thread archived.") {
		t.Fatalf("missing thread archived thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Thread unarchived.") {
		t.Fatalf("missing thread unarchived thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Thread closed.") {
		t.Fatalf("missing thread closed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Thread name updated: ACP Mapping Thread.") {
		t.Fatalf("missing thread name updated thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Windows world-writable warning: 2 additional path(s). sample=C:/tmp/a,C:/tmp/b") {
		t.Fatalf("missing windows world-writable warning thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Windows sandbox setup succeeded (mode=sandbox).") {
		t.Fatalf("missing windows sandbox setup thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime started (version=v1, session=rt-1).") {
		t.Fatalf("missing realtime started thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime item added (type=message).") {
		t.Fatalf("missing realtime item added thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime audio delta: item=rt-item-1 sampleRate=24000 channels=1 bytes=4.") {
		t.Fatalf("missing realtime audio delta thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime transcript (assistant): hello realtime") {
		t.Fatalf("missing realtime transcript thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime error: transport issue") {
		t.Fatalf("missing realtime error thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Realtime closed: done") {
		t.Fatalf("missing realtime closed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Filesystem changed: watch=watch-1 paths=2.") {
		t.Fatalf("missing fs changed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Fuzzy search update: session=fuzzy-1 query=\"agent\" files=1.") {
		t.Fatalf("missing fuzzy session updated thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Fuzzy search completed: session=fuzzy-1.") {
		t.Fatalf("missing fuzzy session completed thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "command/exec output: process=proc-1 stream=stdout bytes(base64)=4 capReached=false.") {
		t.Fatalf("missing command/exec output delta thought update: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "Turn diff updated:\n@@ -1 +1 @@\n-foo\n+bar\n  tail") {
		t.Fatalf("missing turn diff update with preserved formatting: %#v", updates)
	}
	if !containsThoughtSubstring(updates, "MCP server") {
		t.Fatalf("missing mcp status thought update: %#v", updates)
	}
	meta, ok := promptResp.Meta.(map[string]any)
	if !ok {
		t.Fatalf("PromptResponse.Meta type = %T, want map[string]any", promptResp.Meta)
	}
	if _, ok := meta["usage"]; !ok {
		t.Fatalf("PromptResponse.Meta.usage missing: %#v", promptResp.Meta)
	}
	if _, ok := meta["rateLimits"]; !ok {
		t.Fatalf("PromptResponse.Meta.rateLimits missing: %#v", promptResp.Meta)
	}
}

func TestPromptStopsOnErrorNotificationWithoutRetry(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "error", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"willRetry": false,
		"error": map[string]any{
			"message":           "fatal boom",
			"additionalDetails": "stacktrace",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonRefusal {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonRefusal)
	}
	updates := conn.sessionUpdates(newResp.SessionId)
	if !containsThoughtSubstring(updates, "fatal boom") {
		t.Fatalf("missing error thought update: %#v", updates)
	}
}

func TestPromptBridgesToolCallRequestAsPartialForward(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/tool/call", json.RawMessage("3"), map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"callId":   "call-1",
		"tool":     "relay.agents.start",
		"arguments": map[string]any{
			"agent_name": "planner",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got, ok := responses[0]["success"].(bool); !ok || got {
		t.Fatalf("response.success = %v, want false", responses[0]["success"])
	}
	contentItems, ok := responses[0]["contentItems"].([]any)
	if !ok || len(contentItems) == 0 {
		t.Fatalf("response.contentItems missing: %#v", responses[0])
	}
	first, ok := contentItems[0].(map[string]any)
	if !ok {
		t.Fatalf("response.contentItems[0] type = %T, want map[string]any", contentItems[0])
	}
	if got := stringValue(first, "type"); got != "inputText" {
		t.Fatalf("response.contentItems[0].type = %q, want %q", got, "inputText")
	}
	if got := stringValue(first, "text"); !strings.Contains(got, "not executed by ACP bridge") {
		t.Fatalf("response.contentItems[0].text = %q, want contains %q", got, "not executed by ACP bridge")
	}
}

func TestPromptBridgesToolRequestUserInputRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/tool/requestUserInput", json.RawMessage("4"), map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-ui-1",
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"id":       "scope",
				"question": "Which scope?",
				"options": []any{
					map[string]any{"label": "Tier1", "description": "must-have only"},
					map[string]any{"label": "Tier1+2", "description": "must + should"},
				},
			},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	answers, ok := responses[0]["answers"].(map[string]any)
	if !ok {
		t.Fatalf("response.answers type = %T, want map[string]any", responses[0]["answers"])
	}
	scopeAnswer, ok := answers["scope"].(map[string]any)
	if !ok {
		t.Fatalf("response.answers.scope type = %T, want map[string]any", answers["scope"])
	}
	answerList, ok := scopeAnswer["answers"].([]string)
	if !ok {
		rawList, okAny := scopeAnswer["answers"].([]any)
		if !okAny {
			t.Fatalf("response.answers.scope.answers type = %T, want []string/[]any", scopeAnswer["answers"])
		}
		answerList = make([]string, 0, len(rawList))
		for _, raw := range rawList {
			s, _ := raw.(string)
			answerList = append(answerList, s)
		}
	}
	if len(answerList) != 1 || answerList[0] != "Tier1" {
		t.Fatalf("response.answers.scope.answers = %#v, want [Tier1]", answerList)
	}
}

func TestPromptBridgesMcpElicitationRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "mcpServer/elicitation/request", json.RawMessage("5"), map[string]any{
		"serverName": "docs",
		"threadId":   "thr-1",
		"mode":       "form",
		"message":    "Need user input",
		"requestedSchema": map[string]any{
			"type": "object",
		},
		"_meta": map[string]any{
			"trace": "abc",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "action"); got != "accept" {
		t.Fatalf("response.action = %q, want %q", got, "accept")
	}
	if _, ok := responses[0]["content"]; !ok {
		t.Fatalf("response.content missing for accept action: %#v", responses[0])
	}
	if _, ok := responses[0]["_meta"]; !ok {
		t.Fatalf("response._meta missing passthrough: %#v", responses[0])
	}
}

func TestPromptBridgesApplyPatchApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "applyPatchApproval", json.RawMessage("6"), map[string]any{
		"callId":         "call-apply-1",
		"conversationId": "thr-1",
		"fileChanges": map[string]any{
			"README.md": map[string]any{"kind": "modify"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-2"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "decision"); got != "approved_for_session" {
		t.Fatalf("response.decision = %q, want %q", got, "approved_for_session")
	}
}

func TestPromptBridgesExecCommandApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "execCommandApproval", json.RawMessage("7"), map[string]any{
		"callId":         "call-exec-1",
		"conversationId": "thr-1",
		"command":        []any{"curl", "example.com"},
		"cwd":            "/tmp/work",
		"parsedCmd":      []any{},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-3"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "decision"); got != "denied" {
		t.Fatalf("response.decision = %q, want %q", got, "denied")
	}
}

func TestPromptBridgesChatgptAuthTokensRefreshFromEnv(t *testing.T) {
	t.Setenv("CODEX_CHATGPT_ACCESS_TOKEN", "token-1")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "acct-1")
	t.Setenv("CODEX_CHATGPT_PLAN_TYPE", "plus")

	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "account/chatgptAuthTokens/refresh", json.RawMessage("8"), map[string]any{
		"reason": "expired",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "accessToken"); got != "token-1" {
		t.Fatalf("response.accessToken = %q, want %q", got, "token-1")
	}
	if got := stringValue(responses[0], "chatgptAccountId"); got != "acct-1" {
		t.Fatalf("response.chatgptAccountId = %q, want %q", got, "acct-1")
	}
	if got := stringValue(responses[0], "chatgptPlanType"); got != "plus" {
		t.Fatalf("response.chatgptPlanType = %q, want %q", got, "plus")
	}
}

func TestPromptBridgesChatgptAuthTokensRefreshUnavailable(t *testing.T) {
	t.Setenv("CODEX_CHATGPT_ACCESS_TOKEN", "")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "")

	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "account/chatgptAuthTokens/refresh", json.RawMessage("9"), map[string]any{
		"reason": "expired",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	errResponses := session.errorResponsesSnapshot()
	if len(errResponses) != 1 {
		t.Fatalf("error responses = %d, want 1", len(errResponses))
	}
	if got := stringValue(errResponses[0], "message"); !strings.Contains(got, "chatgpt token refresh unavailable") {
		t.Fatalf("error message = %v, want contains %q", got, "chatgpt token refresh unavailable")
	}
}

func TestServerRequestResolvedClearsPendingRequest(t *testing.T) {
	sessionID := acp.SessionId("s1")
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, errors.New("not used")
	}, "agent", codexAppConfig{}, &l)
	agent.sessions[sessionID] = &codexProxySessionState{
		threadID:         "thr-1",
		turnID:           "turn-1",
		pendingRequests:  map[string]string{"1": "item/tool/call"},
		planDeltaByItem:  map[string]string{},
		latestRateLimits: map[string]any{},
	}

	raw, err := json.Marshal(map[string]any{
		"threadId":  "thr-1",
		"requestId": 1,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	done, stopReason, usage, err := agent.handleNotification(context.Background(), sessionID, "thr-1", "turn-1", &appServerNotification{
		Method: "serverRequest/resolved",
		Params: raw,
	})
	if err != nil {
		t.Fatalf("handleNotification() error = %v", err)
	}
	if done {
		t.Fatalf("handleNotification() done = %v, want %v", done, false)
	}
	if stopReason != "" {
		t.Fatalf("handleNotification() stopReason = %q, want empty", stopReason)
	}
	if usage != nil {
		t.Fatalf("handleNotification() usage = %#v, want nil", usage)
	}
	if got := len(agent.sessions[sessionID].pendingRequests); got != 0 {
		t.Fatalf("pending requests = %d, want 0", got)
	}
}

type fakeACPAppConnection struct {
	mu                 sync.Mutex
	permissionResponse acp.RequestPermissionResponse
	permissionError    error
	permissionRequests []acp.RequestPermissionRequest
	updates            []acp.SessionNotification
}

func (f *fakeACPAppConnection) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, params)
	return nil
}

func (f *fakeACPAppConnection) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionRequests = append(f.permissionRequests, params)
	if f.permissionError != nil {
		return acp.RequestPermissionResponse{}, f.permissionError
	}
	if f.permissionResponse.Outcome.Cancelled == nil && f.permissionResponse.Outcome.Selected == nil {
		if len(params.Options) > 0 {
			return acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(params.Options[0].OptionId),
			}, nil
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return f.permissionResponse, nil
}

func (f *fakeACPAppConnection) sessionUpdates(sessionID acp.SessionId) []acp.SessionNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	filtered := make([]acp.SessionNotification, 0, len(f.updates))
	for _, update := range f.updates {
		if update.SessionId == sessionID {
			filtered = append(filtered, update)
		}
	}
	return filtered
}

type fakeAppServerSession struct {
	mu sync.Mutex

	initializeResp appServerInitializeResponse
	events         chan appServerEvent

	threadStartResp appServerThreadStartResponse
	turnStartResp   appServerTurnStartResponse

	threadStartParams []map[string]any
	turnStartParams   []map[string]any
	responses         []map[string]any
	errorResponses    []map[string]any
}

func newFakeAppServerSession(userAgent string, threadID string, turnID string) *fakeAppServerSession {
	threadResp := appServerThreadStartResponse{}
	threadResp.Thread.ID = threadID
	turnResp := appServerTurnStartResponse{}
	turnResp.Turn.ID = turnID
	return &fakeAppServerSession{
		initializeResp:  appServerInitializeResponse{UserAgent: userAgent},
		events:          make(chan appServerEvent, 64),
		threadStartResp: threadResp,
		turnStartResp:   turnResp,
	}
}

func (f *fakeAppServerSession) InitializeResponse() appServerInitializeResponse {
	return f.initializeResp
}

func (f *fakeAppServerSession) Events() <-chan appServerEvent {
	return f.events
}

func (f *fakeAppServerSession) ThreadStart(_ context.Context, params map[string]any) (appServerThreadStartResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadStartParams = append(f.threadStartParams, params)
	return f.threadStartResp, nil
}

func (f *fakeAppServerSession) TurnStart(_ context.Context, params map[string]any) (appServerTurnStartResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnStartParams = append(f.turnStartParams, params)
	return f.turnStartResp, nil
}

func (f *fakeAppServerSession) TurnInterrupt(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeAppServerSession) RespondRequest(_ context.Context, _ *appServerRequest, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := result.(map[string]any)
	if !ok {
		return errors.New("result must be map")
	}
	f.responses = append(f.responses, m)
	return nil
}

func (f *fakeAppServerSession) RespondRequestError(_ context.Context, _ *appServerRequest, code int, message string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errorResponses = append(f.errorResponses, map[string]any{
		"code":    code,
		"message": message,
		"data":    data,
	})
	return nil
}

func (f *fakeAppServerSession) Close() error { return nil }
func (f *fakeAppServerSession) Wait() error  { return nil }

func (f *fakeAppServerSession) responsesSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.responses))
	copy(out, f.responses)
	return out
}

func (f *fakeAppServerSession) errorResponsesSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.errorResponses))
	copy(out, f.errorResponses)
	return out
}

func queueNotification(session *fakeAppServerSession, method string, params map[string]any) {
	raw, _ := json.Marshal(params)
	session.events <- appServerEvent{
		Notification: &appServerNotification{
			Method: method,
			Params: raw,
		},
	}
}

func queueRequest(session *fakeAppServerSession, method string, id json.RawMessage, params map[string]any) {
	raw, _ := json.Marshal(params)
	session.events <- appServerEvent{
		Request: &appServerRequest{
			ID:     id,
			Method: method,
			Params: raw,
		},
	}
}

func containsAgentMessageText(updates []acp.SessionNotification, text string) bool {
	for _, update := range updates {
		if chunk := update.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			if chunk.Content.Text.Text == text {
				return true
			}
		}
	}
	return false
}

func containsPlanEntry(updates []acp.SessionNotification, step string) bool {
	for _, update := range updates {
		if plan := update.Update.Plan; plan != nil {
			for _, entry := range plan.Entries {
				if entry.Content == step {
					return true
				}
			}
		}
	}
	return false
}

func containsToolCall(updates []acp.SessionNotification, toolCallID string) bool {
	for _, update := range updates {
		if call := update.Update.ToolCall; call != nil && string(call.ToolCallId) == toolCallID {
			return true
		}
		if callUpdate := update.Update.ToolCallUpdate; callUpdate != nil && string(callUpdate.ToolCallId) == toolCallID {
			return true
		}
	}
	return false
}

func containsToolCallText(updates []acp.SessionNotification, text string) bool {
	for _, update := range updates {
		callUpdate := update.Update.ToolCallUpdate
		if callUpdate == nil {
			continue
		}
		for _, content := range callUpdate.Content {
			if content.Content == nil || content.Content.Content.Text == nil {
				continue
			}
			if content.Content.Content.Text.Text == text {
				return true
			}
		}
	}
	return false
}

func containsThoughtSubstring(updates []acp.SessionNotification, substring string) bool {
	for _, update := range updates {
		if chunk := update.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
			if strings.Contains(chunk.Content.Text.Text, substring) {
				return true
			}
		}
	}
	return false
}

func countThoughtText(updates []acp.SessionNotification, text string) int {
	count := 0
	for _, update := range updates {
		if chunk := update.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
			if chunk.Content.Text.Text == text {
				count++
			}
		}
	}
	return count
}
