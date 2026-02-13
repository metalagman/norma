package config

import "testing"

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
				Features: map[string]FeatureConfig{
					"backlog_refiner": {
						Agents: map[string]string{
							"planner": "opencode_exec_agent",
						},
					},
				},
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
}

func TestResolveAgents_AllowsUnusedButValidFeatureReferences(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"codex_primary": {Type: "codex"},
			"gemini_flash":  {Type: "gemini"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "codex_primary",
					Do:    "codex_primary",
					Check: "codex_primary",
					Act:   "codex_primary",
				},
				Features: map[string]FeatureConfig{
					"docs_audit": {
						Agents: map[string]string{
							"reviewer": "gemini_flash",
						},
					},
				},
			},
		},
	}

	_, _, err := cfg.ResolveAgents("")
	if err != nil {
		t.Fatalf("ResolveAgents returned error: %v", err)
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

func TestResolveAgents_ReturnsErrorForUndefinedFeatureAgentReference(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"defined": {Type: "codex"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "defined",
					Do:    "defined",
					Check: "defined",
					Act:   "defined",
				},
				Features: map[string]FeatureConfig{
					"extra_tools": {
						Agents: map[string]string{
							"summarizer": "missing",
						},
					},
				},
			},
		},
	}

	_, _, err := cfg.ResolveAgents("")
	if err == nil {
		t.Fatal("ResolveAgents returned nil error, want error")
	}
}

func TestResolveFeatureAgents_ResolvesFeatureAgentMap(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"codex_primary": {Type: "codex", Model: "gpt-5-codex"},
			"gemini_flash":  {Type: "gemini", Model: "gemini-3-flash-preview"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "codex_primary",
					Do:    "codex_primary",
					Check: "codex_primary",
					Act:   "codex_primary",
				},
				Features: map[string]FeatureConfig{
					"backlog_refiner": {
						Agents: map[string]string{
							"planner":     "codex_primary",
							"implementer": "gemini_flash",
						},
					},
				},
			},
		},
	}

	profile, agents, err := cfg.ResolveFeatureAgents("", "backlog_refiner")
	if err != nil {
		t.Fatalf("ResolveFeatureAgents returned error: %v", err)
	}
	if profile != "default" {
		t.Fatalf("profile = %q, want %q", profile, "default")
	}
	if agents["planner"].Type != "codex" {
		t.Fatalf("planner type = %q, want %q", agents["planner"].Type, "codex")
	}
	if agents["implementer"].Type != "gemini" {
		t.Fatalf("implementer type = %q, want %q", agents["implementer"].Type, "gemini")
	}
}

func TestResolveFeatureAgents_ReturnsErrorForMissingFeature(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Agents: map[string]AgentConfig{
			"codex_primary": {Type: "codex"},
		},
		Profiles: map[string]ProfileConfig{
			"default": {
				PDCA: PDCAAgentRefs{
					Plan:  "codex_primary",
					Do:    "codex_primary",
					Check: "codex_primary",
					Act:   "codex_primary",
				},
			},
		},
	}

	_, _, err := cfg.ResolveFeatureAgents("", "backlog_refiner")
	if err == nil {
		t.Fatal("ResolveFeatureAgents returned nil error, want error")
	}
}

func TestValidateSettings_AllowsOpenAIAgentWithAPIKeyEnv(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"openai_primary": map[string]any{
				"type":        AgentTypeOpenAI,
				"model":       "gpt-5",
				"api_key_env": "OPENAI_API_KEY",
				"timeout":     45,
			},
		},
		"profiles": map[string]any{
			"default": map[string]any{
				"pdca": map[string]any{
					"plan":  "openai_primary",
					"do":    "openai_primary",
					"check": "openai_primary",
					"act":   "openai_primary",
				},
			},
		},
		"budgets": map[string]any{
			"max_iterations": 5,
		},
		"retention": map[string]any{
			"keep_last": 10,
			"keep_days": 7,
		},
	}

	if err := ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings returned error: %v", err)
	}
}

func TestValidateSettings_RejectsOpenAIAgentWithoutAPIKey(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"agents": map[string]any{
			"openai_primary": map[string]any{
				"type":  AgentTypeOpenAI,
				"model": "gpt-5",
			},
		},
		"profiles": map[string]any{
			"default": map[string]any{
				"pdca": map[string]any{
					"plan":  "openai_primary",
					"do":    "openai_primary",
					"check": "openai_primary",
					"act":   "openai_primary",
				},
			},
		},
		"budgets": map[string]any{
			"max_iterations": 1,
		},
	}

	if err := ValidateSettings(settings); err == nil {
		t.Fatal("ValidateSettings returned nil error, want error")
	}
}
