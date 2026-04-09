package mcpregistry

import (
	"testing"

	"github.com/normahq/norma/pkg/runtime/agentconfig"
)

func TestMapRegistryDelete(t *testing.T) {
	reg := New(map[string]agentconfig.MCPServerConfig{
		"relay": {Type: agentconfig.MCPServerTypeHTTP, URL: "http://127.0.0.1:9090/mcp"},
	})

	reg.Delete("relay")

	if _, ok := reg.Get("relay"); ok {
		t.Fatal("Get(relay) = ok, want deleted entry")
	}
}
