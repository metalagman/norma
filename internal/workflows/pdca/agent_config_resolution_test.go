package pdca

import (
	"strings"
	"testing"

	"github.com/metalagman/norma/internal/config"
)

func TestResolvedAgentForRole_ReturnsConfig(t *testing.T) {
	t.Parallel()

	agents := map[string]config.AgentConfig{
		"plan": {Type: "codex", Model: "gpt-5.2-codex"},
	}

	got, err := resolvedAgentForRole(agents, "plan")
	if err != nil {
		t.Fatalf("resolvedAgentForRole returned error: %v", err)
	}
	if got.Type != "codex" {
		t.Fatalf("agent type = %q, want %q", got.Type, "codex")
	}
}

func TestResolvedAgentForRole_ReturnsRoleSpecificError(t *testing.T) {
	t.Parallel()

	_, err := resolvedAgentForRole(map[string]config.AgentConfig{}, "act")
	if err == nil {
		t.Fatal("resolvedAgentForRole returned nil error, want error")
	}
	if !strings.Contains(err.Error(), `role "act"`) {
		t.Fatalf("error %q does not include missing role", err.Error())
	}
}
