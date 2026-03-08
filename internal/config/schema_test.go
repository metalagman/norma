package config

import "testing"

func TestValidateSettings_AcceptACPTypes(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"planner": map[string]any{
				"type":  "gemini_acp",
				"model": "gemini-3-flash-preview",
			},
			"worker": map[string]any{
				"type": "acp_exec",
				"cmd":  []string{"custom-acp-cli", "--acp"},
			},
		},
		"profiles": map[string]any{
			"default": map[string]any{
				"pdca": map[string]any{
					"plan":  "planner",
					"do":    "worker",
					"check": "worker",
					"act":   "worker",
				},
				"planner": "planner",
			},
		},
		"budgets": map[string]any{
			"max_iterations": 1,
		},
	}

	if err := ValidateSettings(settings); err != nil {
		t.Fatalf("ValidateSettings returned error: %v", err)
	}
}

func TestValidateSettings_ACPExecRequiresCmd(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"worker": map[string]any{
				"type": "acp_exec",
			},
		},
		"profiles": map[string]any{
			"default": map[string]any{
				"pdca": map[string]any{
					"plan":  "worker",
					"do":    "worker",
					"check": "worker",
					"act":   "worker",
				},
			},
		},
		"budgets": map[string]any{
			"max_iterations": 1,
		},
	}

	if err := ValidateSettings(settings); err == nil {
		t.Fatal("ValidateSettings returned nil error, want cmd validation error")
	}
}
