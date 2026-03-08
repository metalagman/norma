package config

import (
	"testing"
)

const opencodeType = "opencode"

func TestResolveAgents_ResolvesPDCARolesFromGlobalAgents(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"opencode_exec_agent": {Type: opencodeType, Model: "opencode/big-pickle"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "opencode_exec_agent",
					Do:    "opencode_exec_agent",
					Check: "opencode_exec_agent",
					Act:   "opencode_exec_agent",
				},
				Planner: "opencode_exec_agent",
			},
		},
	}

	profile, agents, err := cfg.ResolveAgents("")
	if err != nil {
		t.Fatalf("ResolveAgents returned error: %v", err)
	}
	if profile != "default" {
		t.Fatalf("profile = %q, want %q", profile, "default")
	}
	if agents["plan"].Type != opencodeType {
		t.Fatalf("plan agent type = %q, want %q", agents["plan"].Type, opencodeType)
	}
	if agents["do"].Type != opencodeType {
		t.Fatalf("do agent type = %q, want %q", agents["do"].Type, opencodeType)
	}
	if agents["check"].Type != opencodeType {
		t.Fatalf("check agent type = %q, want %q", agents["check"].Type, opencodeType)
	}
	if agents["act"].Type != opencodeType {
		t.Fatalf("act agent type = %q, want %q", agents["act"].Type, opencodeType)
	}
	if agents["planner"].Type != opencodeType {
		t.Fatalf("planner agent type = %q, want %q", agents["planner"].Type, opencodeType)
	}
}

func TestResolveAgents_ReturnsErrorForUndefinedAgentReference(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"defined": {Type: "codex"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "defined",
					Do:    "missing",
					Check: "defined",
					Act:   "defined",
				},
			},
		},
	}

	_, _, err := cfg.ResolveAgents("")
	if err == nil {
		t.Fatal("ResolveAgents returned nil error, want error")
	}
}

func TestIsACPType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  string
		want bool
	}{
		{typ: AgentTypeACPExec, want: true},
		{typ: AgentTypeGeminiACP, want: true},
		{typ: AgentTypeOpenCodeACP, want: true},
		{typ: AgentTypeCodexACP, want: true},
		{typ: AgentTypeExec, want: false},
		{typ: AgentTypeCodex, want: false},
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
