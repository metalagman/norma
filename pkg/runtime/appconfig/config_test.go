package appconfig

import (
	"testing"
)

func TestLegacyNormaRootKeyFails(t *testing.T) {
	fullDocument := map[string]any{
		"norma": map[string]any{
			"providers": map[string]any{
				"claude": map[string]any{
					"type": "claude_code_acp",
					"claude_code_acp": map[string]any{
						"model": "claude-sonnet-4",
					},
				},
			},
		},
	}

	err := ValidateSettings(fullDocument)
	if err == nil {
		t.Fatal("expected error for legacy 'norma' root key, got nil")
	}

	expectedMsg := "legacy root key 'norma' detected"
	if !contains(err.Error(), expectedMsg) {
		t.Fatalf("error message should contain %q, got: %s", expectedMsg, err.Error())
	}

	expectedHint := "migrate to 'runtime'"
	if !contains(err.Error(), expectedHint) {
		t.Fatalf("error message should contain migration hint %q, got: %s", expectedHint, err.Error())
	}
}

func TestRuntimeRootKeySucceeds(t *testing.T) {
	runtimeSection := map[string]any{
		"providers": map[string]any{
			"claude": map[string]any{
				"type": "claude_code_acp",
				"claude_code_acp": map[string]any{
					"model": "claude-sonnet-4",
				},
			},
		},
	}

	err := ValidateSettings(runtimeSection)
	if err != nil {
		t.Fatalf("expected no error for 'runtime' root key, got: %s", err.Error())
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
