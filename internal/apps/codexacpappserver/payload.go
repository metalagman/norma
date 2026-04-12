package codexacp

import (
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

const (
	statusInProgress = "inProgress"
	statusCompleted  = "completed"
	statusFailed     = "failed"
)

func joinPromptText(blocks []acp.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, block := range blocks {
		if block.Text == nil {
			continue
		}
		trimmed := strings.TrimSpace(block.Text.Text)
		if trimmed == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(trimmed)
	}
	return builder.String()
}

func buildThreadStartParams(cwd string, cfg codexAppConfig, sessionModel string, sessionMCPServers map[string]acp.McpServer) map[string]any {
	params := map[string]any{
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
	}
	if trimmedCWD := strings.TrimSpace(cwd); trimmedCWD != "" {
		params["cwd"] = trimmedCWD
	}

	effectiveCfg := cfg.withModel(sessionModel)
	if model := strings.TrimSpace(effectiveCfg.Model); model != "" {
		params["model"] = model
	}
	if approval := strings.TrimSpace(effectiveCfg.ApprovalPolicy); approval != "" {
		params["approvalPolicy"] = approval
	}
	if sandbox := strings.TrimSpace(effectiveCfg.Sandbox); sandbox != "" {
		params["sandbox"] = sandbox
	}
	if baseInstructions := strings.TrimSpace(effectiveCfg.BaseInstructions); baseInstructions != "" {
		params["baseInstructions"] = baseInstructions
	}
	if developerInstructions := strings.TrimSpace(effectiveCfg.DeveloperInstructions); developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}

	config := cloneMap(effectiveCfg.Config)
	if config == nil {
		config = map[string]any{}
	}
	if profile := strings.TrimSpace(effectiveCfg.Profile); profile != "" {
		config["profile"] = profile
	}
	if compactPrompt := strings.TrimSpace(effectiveCfg.CompactPrompt); compactPrompt != "" {
		config["compact_prompt"] = compactPrompt
	}
	if mcpServersCfg := codexMCPServersConfig(sessionMCPServers); len(mcpServersCfg) > 0 {
		config["mcp_servers"] = mcpServersCfg
	}
	if len(config) > 0 {
		params["config"] = config
	}

	return params
}

func buildTurnStartParams(threadID string, prompt string, model string) map[string]any {
	params := map[string]any{
		"threadId": strings.TrimSpace(threadID),
		"input": []any{
			map[string]any{
				"type":          "text",
				"text":          prompt,
				"text_elements": []any{},
			},
		},
	}
	if trimmedModel := strings.TrimSpace(model); trimmedModel != "" {
		params["model"] = trimmedModel
	}
	return params
}

func codexMCPServersConfig(sessionMCPServers map[string]acp.McpServer) map[string]any {
	if len(sessionMCPServers) == 0 {
		return nil
	}
	result := make(map[string]any, len(sessionMCPServers))
	for name, server := range sessionMCPServers {
		serverCfg := map[string]any{}
		switch {
		case server.Stdio != nil:
			serverCfg["command"] = server.Stdio.Command
			if len(server.Stdio.Args) > 0 {
				serverCfg["args"] = server.Stdio.Args
			}
			if env := flattenEnvVars(server.Stdio.Env); len(env) > 0 {
				serverCfg["env"] = env
			}
		case server.Http != nil:
			serverCfg["url"] = server.Http.Url
			if headers := flattenHTTPHeaders(server.Http.Headers); len(headers) > 0 {
				serverCfg["http_headers"] = headers
			}
		default:
			continue
		}
		if len(serverCfg) > 0 {
			result[name] = serverCfg
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func toAppServerToolKind(itemType string) acp.ToolKind {
	switch itemType {
	case "commandExecution":
		return acp.ToolKindExecute
	case "fileChange":
		return acp.ToolKindEdit
	case "webSearch":
		return acp.ToolKindFetch
	case "mcpToolCall", "dynamicToolCall":
		return acp.ToolKindExecute
	case "imageView":
		return acp.ToolKindRead
	default:
		return acp.ToolKindOther
	}
}

func toACPToolCallStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case statusInProgress:
		return acp.ToolCallStatusInProgress
	case statusCompleted:
		return acp.ToolCallStatusCompleted
	case statusFailed, "declined":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func toACPPlanStatus(status string) acp.PlanEntryStatus {
	switch strings.TrimSpace(status) {
	case "pending":
		return acp.PlanEntryStatusPending
	case statusInProgress:
		return acp.PlanEntryStatusInProgress
	case statusCompleted:
		return acp.PlanEntryStatusCompleted
	default:
		return acp.PlanEntryStatusPending
	}
}
