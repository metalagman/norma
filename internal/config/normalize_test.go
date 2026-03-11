package config

import "testing"

func TestNormalizeAgentAliases(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"acp_alias": {
				Type:  AgentTypeCodexACP,
				Model: "gpt-5-codex",
			},
			"generic_acp": {
				Type: AgentTypeGenericACP,
				Cmd:  []string{"custom-acp"},
			},
		},
	}

	normalized, err := NormalizeAgentAliases(cfg, "/tmp/norma")
	if err != nil {
		t.Fatalf("NormalizeAgentAliases returned error: %v", err)
	}

	acpCfg := normalized.Agents["acp_alias"]
	if acpCfg.Type != AgentTypeGenericACP {
		t.Fatalf("acp_alias type = %q, want %q", acpCfg.Type, AgentTypeGenericACP)
	}
	if len(acpCfg.Cmd) < 3 || acpCfg.Cmd[0] != "/tmp/norma" || acpCfg.Cmd[1] != "proxy" || acpCfg.Cmd[2] != "codex-acp" {
		t.Fatalf("acp_alias cmd = %v, want codex acp proxy command", acpCfg.Cmd)
	}

	genericCfg := normalized.Agents["generic_acp"]
	if genericCfg.Type != AgentTypeGenericACP {
		t.Fatalf("generic_acp type = %q, want %q", genericCfg.Type, AgentTypeGenericACP)
	}
	if len(genericCfg.Cmd) != 1 || genericCfg.Cmd[0] != "custom-acp" {
		t.Fatalf("generic_acp cmd = %v, want %v", genericCfg.Cmd, []string{"custom-acp"})
	}
}
