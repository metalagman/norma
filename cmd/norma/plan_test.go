package main

import (
	"testing"

	"github.com/metalagman/norma/internal/config"
	"github.com/spf13/viper"
)

func TestResolvePlannerAgent_PrefersBacklogRefinerPlanner(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("profile", "default")

	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"codex_primary": {Type: "codex", Model: "gpt-5-codex"},
			"gemini_flash":  {Type: "gemini", Model: "gemini-3-flash-preview"},
		},
		Profiles: map[string]config.ProfileConfig{
			"default": {
				PDCA: config.PDCAAgentRefs{
					Plan:  "gemini_flash",
					Do:    "gemini_flash",
					Check: "gemini_flash",
					Act:   "gemini_flash",
				},
				Planner: "codex_primary",
			},
		},
	}

	agentCfg, err := resolvePlannerAgent(cfg)
	if err != nil {
		t.Fatalf("resolvePlannerAgent returned error: %v", err)
	}
	if agentCfg.Type != "codex" {
		t.Fatalf("resolved planner type = %q, want %q", agentCfg.Type, "codex")
	}
}
