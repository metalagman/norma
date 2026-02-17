package modelfactory_test

import (
	"testing"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/stretchr/testify/assert"
)

func TestFactory_CreateModel(t *testing.T) {
	tests := []struct {
		name    string
		config  modelfactory.FactoryConfig
		target  string
		wantErr string
	}{
		{
			name: "gemini_ok",
			config: modelfactory.FactoryConfig{
				"g1": {
					Type:   modelfactory.ModelTypeGeminiAIStudio,
					Model:  "gemini-1.5-pro",
					APIKey: "key",
				},
			},
			target: "g1",
		},
		{
			name: "openai_ok",
			config: modelfactory.FactoryConfig{
				"o1": {
					Type:   modelfactory.ModelTypeOpenAI,
					Model:  "gpt-4o",
					APIKey: "key",
				},
			},
			target: "o1",
		},
		{
			name: "exec_ok",
			config: modelfactory.FactoryConfig{
				"e1": {
					Type: modelfactory.ModelTypeExec,
					Cmd:  []string{"echo", "hello"},
				},
			},
			target: "e1",
		},
		{
			name: "not_found",
			config: modelfactory.FactoryConfig{
				"g1": {
					Type:  modelfactory.ModelTypeGeminiAIStudio,
					Model: "gemini-1.5-pro",
				},
			},
			target:  "other",
			wantErr: `model "other" not found`,
		},
		{
			name: "unsupported_filtered",
			config: modelfactory.FactoryConfig{
				"u1": {Type: "unsupported"},
			},
			target:  "u1",
			wantErr: `model "u1" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := modelfactory.NewFactory(tt.config)
			m, err := f.CreateModel(tt.target)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, m)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, m)
			}
		})
	}
}
