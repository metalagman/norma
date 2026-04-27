package config

import (
	"strings"
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
		Runtime: runtimeconfig.RuntimeConfig{
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
}

func TestResolveRoleIDs_ReturnsErrorForUndefinedAgentReference(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
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

func TestResolvePlannerProvider_ResolvesConfiguredProvider(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				opencodeACPAgentID: {
					Type: opencodeACPType,
					OpenCodeACP: &agentconfig.ACPConfig{
						Model: "opencode/big-pickle",
					},
				},
			},
		},
	}

	providerID, err := cfg.ResolvePlannerProvider(PlannerSettings{Provider: opencodeACPAgentID})
	if err != nil {
		t.Fatalf("ResolvePlannerProvider returned error: %v", err)
	}
	if providerID != opencodeACPAgentID {
		t.Fatalf("provider ID = %q, want %q", providerID, opencodeACPAgentID)
	}
}

func TestResolvePlannerProvider_ReturnsErrorForMissingProvider(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"defined": {Type: "gemini_acp", GeminiACP: &agentconfig.ACPConfig{Model: "gemini-3-flash-preview"}},
			},
		},
	}

	_, err := cfg.ResolvePlannerProvider(PlannerSettings{})
	if err == nil {
		t.Fatal("ResolvePlannerProvider returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "planner.provider is required") {
		t.Fatalf("ResolvePlannerProvider error = %q, want missing planner.provider", err)
	}
}

func TestResolvePlannerProvider_ReturnsErrorForUnknownProvider(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"defined": {Type: "gemini_acp", GeminiACP: &agentconfig.ACPConfig{Model: "gemini-3-flash-preview"}},
			},
		},
	}

	_, err := cfg.ResolvePlannerProvider(PlannerSettings{Provider: "missing"})
	if err == nil {
		t.Fatal("ResolvePlannerProvider returned nil error, want error")
	}
	if !strings.Contains(err.Error(), `planner.provider "missing" is not defined in runtime.providers`) {
		t.Fatalf("ResolvePlannerProvider error = %q, want unknown planner.provider", err)
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

func TestResolveSwarmRoles_UsesDefaultAndOverrideProviders(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"codex":  {Type: AgentTypeCodexACP, CodexACP: &agentconfig.ACPConfig{Model: "codex"}},
				"open":   {Type: AgentTypeOpenCodeACP, OpenCodeACP: &agentconfig.ACPConfig{Model: "open"}},
				"gemini": {Type: AgentTypeGeminiACP, GeminiACP: &agentconfig.ACPConfig{Model: "gemini"}},
			},
		},
	}

	roles, err := cfg.ResolveSwarmRoles(SwarmSettings{
		PrimaryRole:     "coordinator",
		DefaultProvider: "codex",
		Roles: map[string]SwarmRoleConfig{
			"coordinator": {Assignee: "norma-coordinator", Instruction: "coordinate"},
			"planner":     {Assignee: "norma-planner", Instruction: "plan", Provider: "gemini"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveSwarmRoles returned error: %v", err)
	}
	if got := roles["coordinator"].ProviderID; got != "codex" {
		t.Fatalf("coordinator provider = %q, want codex", got)
	}
	if got := roles["planner"].ProviderID; got != "gemini" {
		t.Fatalf("planner provider = %q, want gemini", got)
	}
	if !roles["coordinator"].IsPrimaryRole {
		t.Fatal("coordinator IsPrimaryRole = false, want true")
	}
}

func TestResolveSwarmRoles_ReturnsErrorForDuplicateAssignee(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"codex": {Type: AgentTypeCodexACP, CodexACP: &agentconfig.ACPConfig{Model: "codex"}},
			},
		},
	}

	_, err := cfg.ResolveSwarmRoles(SwarmSettings{
		PrimaryRole:     "coordinator",
		DefaultProvider: "codex",
		Roles: map[string]SwarmRoleConfig{
			"coordinator": {Assignee: "norma-role", Instruction: "coordinate"},
			"planner":     {Assignee: "norma-role", Instruction: "plan"},
		},
	})
	if err == nil {
		t.Fatal("ResolveSwarmRoles returned nil error, want duplicate assignee error")
	}
}

func TestResolveSwarmRoles_ReturnsErrorForMissingPrimaryRole(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"codex": {Type: AgentTypeCodexACP, CodexACP: &agentconfig.ACPConfig{Model: "codex"}},
			},
		},
	}

	_, err := cfg.ResolveSwarmRoles(SwarmSettings{
		DefaultProvider: "codex",
		Roles: map[string]SwarmRoleConfig{
			"coordinator": {Assignee: "norma-role", Instruction: "coordinate"},
		},
	})
	if err == nil {
		t.Fatal("ResolveSwarmRoles returned nil error, want missing primary role error")
	}
	if !strings.Contains(err.Error(), "swarm.primary_role is required") {
		t.Fatalf("ResolveSwarmRoles error = %q, want missing swarm.primary_role", err)
	}
}

func TestResolveSwarmRoles_ReturnsErrorForMissingDefaultProvider(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"codex": {Type: AgentTypeCodexACP, CodexACP: &agentconfig.ACPConfig{Model: "codex"}},
			},
		},
	}

	_, err := cfg.ResolveSwarmRoles(SwarmSettings{
		PrimaryRole: "coordinator",
		Roles: map[string]SwarmRoleConfig{
			"coordinator": {Assignee: "norma-role", Instruction: "coordinate"},
		},
	})
	if err == nil {
		t.Fatal("ResolveSwarmRoles returned nil error, want missing default provider error")
	}
	if !strings.Contains(err.Error(), "swarm.default_provider is required") {
		t.Fatalf("ResolveSwarmRoles error = %q, want missing swarm.default_provider", err)
	}
}

func TestResolveSwarmRoles_ReturnsErrorForUnknownProviderReference(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Runtime: runtimeconfig.RuntimeConfig{
			Providers: map[string]AgentConfig{
				"codex": {Type: AgentTypeCodexACP, CodexACP: &agentconfig.ACPConfig{Model: "codex"}},
			},
		},
	}

	_, err := cfg.ResolveSwarmRoles(SwarmSettings{
		PrimaryRole:     "coordinator",
		DefaultProvider: "codex",
		Roles: map[string]SwarmRoleConfig{
			"coordinator": {Assignee: "norma-role", Instruction: "coordinate"},
			"planner":     {Assignee: "norma-planner", Instruction: "plan", Provider: "missing"},
		},
	})
	if err == nil {
		t.Fatal("ResolveSwarmRoles returned nil error, want unknown provider error")
	}
	if !strings.Contains(err.Error(), `swarm.roles.planner.provider "missing" is not defined in runtime.providers`) {
		t.Fatalf("ResolveSwarmRoles error = %q, want unknown provider message", err)
	}
}
