package appconfig

import (
	"testing"
)

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
