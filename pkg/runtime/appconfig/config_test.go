package appconfig

import (
	"testing"
)

func TestLegacyAgentsKeyOnlyConfigFailsWithMigrationHint(t *testing.T) {
	settings := map[string]any{
		"norma": map[string]any{
			"agents": map[string]any{
				"claude": map[string]any{
					"type": "claude_code_acp",
				},
			},
		},
	}

	err := ValidateSettings(settings)
	if err == nil {
		t.Fatal("expected error for legacy 'agents' key, got nil")
	}

	expectedMsg := "legacy 'norma.agents' key detected"
	if !contains(err.Error(), expectedMsg) {
		t.Fatalf("error message should contain %q, got: %s", expectedMsg, err.Error())
	}

	expectedMigration := "migrate to 'norma.providers'"
	if !contains(err.Error(), expectedMigration) {
		t.Fatalf("error message should contain migration hint %q, got: %s", expectedMigration, err.Error())
	}
}

func TestMixedProvidersAndAgentsConfigFailsWithMigrationHint(t *testing.T) {
	settings := map[string]any{
		"norma": map[string]any{
			"providers": map[string]any{
				"claude": map[string]any{
					"type": "claude_code_acp",
				},
			},
			"agents": map[string]any{
				"codex": map[string]any{
					"type": "codex_acp",
				},
			},
		},
	}

	err := ValidateSettings(settings)
	if err == nil {
		t.Fatal("expected error for mixed 'agents' and 'providers' keys, got nil")
	}

	expectedMsg := "legacy 'norma.agents' key detected"
	if !contains(err.Error(), expectedMsg) {
		t.Fatalf("error message should contain %q, got: %s", expectedMsg, err.Error())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
