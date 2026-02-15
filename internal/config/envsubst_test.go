package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandEnvSubstitution(t *testing.T) {
	err := os.Setenv("NORMA_TEST_VAR", "expanded_value")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("NORMA_TEST_VAR")
		require.NoError(t, err)
	}()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple expansion",
			input:    "value: $NORMA_TEST_VAR",
			expected: "value: expanded_value",
		},
		{
			name:     "braced expansion",
			input:    "value: ${NORMA_TEST_VAR}",
			expected: "value: expanded_value",
		},
		{
			name:     "no expansion",
			input:    "value: regular_text",
			expected: "value: regular_text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandEnv(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestExpandEnvSubstitution_RejectsMissingVariables(t *testing.T) {
	const missingVar = "NORMA_TEST_MISSING_VAR"

	err := os.Setenv("NORMA_TEST_VAR", "expanded_value")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("NORMA_TEST_VAR")
		require.NoError(t, err)
	}()

	err = os.Unsetenv(missingVar)
	require.NoError(t, err)

	got, err := ExpandEnv("value: $NORMA_TEST_VAR and ${NORMA_TEST_MISSING_VAR}")
	require.Error(t, err)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), missingVar)
}
