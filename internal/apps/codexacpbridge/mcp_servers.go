package codexacp

import (
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

func validateMCPServers(servers []acp.McpServer) (map[string]acp.McpServer, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	result := make(map[string]acp.McpServer, len(servers))
	for _, server := range servers {
		var name string
		hasStdio := false
		hasHTTP := false
		hasSSE := false

		if server.Stdio != nil {
			hasStdio = true
			name = strings.TrimSpace(server.Stdio.Name)
		}
		if server.Http != nil {
			hasHTTP = true
			name = strings.TrimSpace(server.Http.Name)
		}
		if server.Sse != nil {
			hasSSE = true
			name = strings.TrimSpace(server.Sse.Name)
		}

		transportCount := 0
		if hasStdio {
			transportCount++
		}
		if hasHTTP {
			transportCount++
		}
		if hasSSE {
			transportCount++
		}
		if transportCount == 0 {
			return nil, fmt.Errorf("mcp server must specify exactly one transport")
		}
		if transportCount > 1 {
			if name == "" {
				name = "<unnamed>"
			}
			return nil, fmt.Errorf("mcp server %q: exactly one transport is required", name)
		}

		if hasSSE {
			return nil, fmt.Errorf("mcp server %q: transport 'sse' is not supported, only stdio and http are allowed", name)
		}
		if name == "" {
			return nil, fmt.Errorf("mcp server name is required")
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("mcp server with name %q is duplicated", name)
		}
		result[name] = server
	}

	return result, nil
}

func flattenEnvVars(env []acp.EnvVariable) map[string]string {
	if len(env) == 0 {
		return nil
	}
	result := make(map[string]string, len(env))
	for _, e := range env {
		result[e.Name] = e.Value
	}
	return result
}

func flattenHTTPHeaders(headers []acp.HttpHeader) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for _, h := range headers {
		result[h.Name] = h.Value
	}
	return result
}
