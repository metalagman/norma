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
				"mode":  "code",
			},
			"copilot": map[string]any{
				"type": "copilot_acp",
			},
			"worker": map[string]any{
				"type": "generic_acp",
				"cmd":  []string{"custom-acp-cli", "--acp"},
			},
		},
		"profiles": map[string]any{
			"default": map[string]any{
				"pdca": map[string]any{
					"plan":  "planner",
					"do":    "copilot",
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

func TestValidateSettings_GenericACPRequiresCmd(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"worker": map[string]any{
				"type": "generic_acp",
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

func TestValidateSettings_RejectGenericExec(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"worker": map[string]any{
				"type": "generic_exec",
				"cmd":  []string{"ainvoke"},
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
		t.Fatal("ValidateSettings returned nil error, want type validation error for generic_exec")
	}
}

func TestValidateSettings_RejectGenericExecRequiresCmd(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"worker": map[string]any{
				"type": "generic_exec",
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
		t.Fatal("ValidateSettings returned nil error, want type validation error for generic_exec")
	}
}

func TestValidateSettings_RejectExecType(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"profile": "default",
		"agents": map[string]any{
			"worker": map[string]any{
				"type": "exec",
				"cmd":  []string{"ainvoke"},
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
		t.Fatal("ValidateSettings returned nil error, want type validation error")
	}
}
