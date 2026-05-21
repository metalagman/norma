package plancmd

import (
	"testing"

	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
)

func TestPlannerMCPServersAddsTasksServerAndMergesConfigured(t *testing.T) {
	configured := map[string]config.MCPServerConfig{
		"existing": {
			Type: agentconfig.MCPServerTypeStdio,
			Cmd:  []string{"echo", "ok"},
		},
	}

	servers, err := plannerMCPServers("./repo", configured, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("plannerMCPServers() error = %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(servers))
	}

	existing, ok := servers["existing"]
	if !ok {
		t.Fatalf("servers missing configured entry %q", "existing")
	}
	if existing.Type != agentconfig.MCPServerTypeStdio {
		t.Fatalf("existing.Type = %q, want %q", existing.Type, agentconfig.MCPServerTypeStdio)
	}
	if len(existing.Cmd) != 2 || existing.Cmd[0] != "echo" || existing.Cmd[1] != "ok" {
		t.Fatalf("existing.Cmd = %v, want %v", existing.Cmd, []string{"echo", "ok"})
	}

	tasksCfg, ok := servers[plannerTasksMCPName]
	if !ok {
		t.Fatalf("servers missing planner tasks MCP entry %q", plannerTasksMCPName)
	}
	if tasksCfg.Type != agentconfig.MCPServerTypeHTTP {
		t.Fatalf("tasksCfg.Type = %q, want %q", tasksCfg.Type, agentconfig.MCPServerTypeHTTP)
	}
	if tasksCfg.URL != "http://127.0.0.1:12345" {
		t.Fatalf("tasksCfg.URL = %q, want %q", tasksCfg.URL, "http://127.0.0.1:12345")
	}
}

func TestPlannerMCPServersUsesHTTPTransport(t *testing.T) {
	servers, err := plannerMCPServers(".", nil, "localhost:8080")
	if err != nil {
		t.Fatalf("plannerMCPServers() error = %v", err)
	}

	tasksCfg, ok := servers[plannerTasksMCPName]
	if !ok {
		t.Fatalf("servers missing planner tasks MCP entry %q", plannerTasksMCPName)
	}
	if tasksCfg.Type != agentconfig.MCPServerTypeHTTP {
		t.Fatalf("tasksCfg.Type = %q, want %q", tasksCfg.Type, agentconfig.MCPServerTypeHTTP)
	}
	if tasksCfg.URL != "http://localhost:8080" {
		t.Fatalf("tasksCfg.URL = %q, want %q", tasksCfg.URL, "http://localhost:8080")
	}
}
