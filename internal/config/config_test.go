package config

import (
	"testing"

	"github.com/normahq/norma/pkg/runtime/agentconfig"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
)

const (
	opencodeACPType    = "opencode_acp"
	opencodeACPAgentID = "opencode_acp_agent"
)

func TestResolveRoleIDs_ResolvesPDCARolesFromGlobalAgents(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Norma: runtimeconfig.NormaConfig{
			Providers: map[string]AgentConfig{
				opencodeACPAgentID: {
					Type: opencodeACPType,
					OpenCodeACP: &agentconfig.ACPConfig{
						Model: "opencode/big-pickle",
					},
				},
			},
		},
		Profile: "default",
	}

	agentIDs, err := cfg.ResolveRoleIDs(CLISettings{
		PDCA: PDCAAgentRefs{
			Plan:  opencodeACPAgentID,
			Do:    opencodeACPAgentID,
			Check: opencodeACPAgentID,
			Act:   opencodeACPAgentID,
		},
		Planner: opencodeACPAgentID,
	})
	if err != nil {
		t.Fatalf("ResolveRoleIDs returned error: %v", err)
	}
	if agentIDs["plan"] != opencodeACPAgentID {
		t.Fatalf("plan agent ID = %q, want %q", agentIDs["plan"], opencodeACPAgentID)
	}
	if agentIDs["do"] != opencodeACPAgentID {
		t.Fatalf("do agent ID = %q, want %q", agentIDs["do"], opencodeACPAgentID)
	}
	if agentIDs["check"] != opencodeACPAgentID {
		t.Fatalf("check agent ID = %q, want %q", agentIDs["check"], opencodeACPAgentID)
	}
	if agentIDs["act"] != opencodeACPAgentID {
		t.Fatalf("act agent ID = %q, want %q", agentIDs["act"], opencodeACPAgentID)
	}
	if agentIDs["planner"] != opencodeACPAgentID {
		t.Fatalf("planner agent ID = %q, want %q", agentIDs["planner"], opencodeACPAgentID)
	}
}

func TestResolveRoleIDs_ReturnsErrorForUndefinedAgentReference(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Norma: runtimeconfig.NormaConfig{
			Providers: map[string]AgentConfig{
				"defined": {Type: "gemini_acp", GeminiACP: &agentconfig.ACPConfig{Model: "gemini-3-flash-preview"}},
			},
		},
		Profile: "default",
	}

	_, err := cfg.ResolveRoleIDs(CLISettings{
		PDCA: PDCAAgentRefs{
			Plan:  "defined",
			Do:    "missing",
			Check: "defined",
			Act:   "defined",
		},
	})
	if err == nil {
		t.Fatal("ResolveRoleIDs returned nil error, want error")
	}
}

func TestIsACPType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  string
		want bool
	}{
		{typ: AgentTypeGenericACP, want: true},
		{typ: AgentTypeGeminiACP, want: true},
		{typ: AgentTypeOpenCodeACP, want: true},
		{typ: AgentTypeCodexACP, want: true},
		{typ: AgentTypeCopilotACP, want: true},
		{typ: AgentTypeClaudeCodeACP, want: true},
		{typ: "generic_exec", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()
			if got := IsACPType(tc.typ); got != tc.want {
				t.Fatalf("IsACPType(%q) = %t, want %t", tc.typ, got, tc.want)
			}
		})
	}
}

func TestIsPlannerSupportedType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  string
		want bool
	}{
		{typ: AgentTypeGenericACP, want: true},
		{typ: AgentTypeCodexACP, want: true},
		{typ: AgentTypeCopilotACP, want: true},
		{typ: AgentTypeClaudeCodeACP, want: true},
		{typ: "generic_exec", want: false},
		{typ: "unknown", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()
			if got := IsPlannerSupportedType(tc.typ); got != tc.want {
				t.Fatalf("IsPlannerSupportedType(%q) = %t, want %t", tc.typ, got, tc.want)
			}
		})
	}
}
