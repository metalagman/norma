package agentfactory_test

import (
	"testing"

	"github.com/metalagman/norma/internal/agentfactory"
	"github.com/stretchr/testify/assert"
)

func TestFactory_CreateModel(t *testing.T) {
	tests := []struct {
		name    string
		config  agentfactory.FactoryConfig
		target  string
		wantErr string
	}{
		{
			name: "gemini_ok",
			config: agentfactory.FactoryConfig{
				"g1": {
					Type:   agentfactory.AgentTypeGeminiAIStudio,
					Model:  "gemini-1.5-pro",
					APIKey: "key",
				},
			},
			target: "g1",
		},
		{
			name: "openai_ok",
			config: agentfactory.FactoryConfig{
				"o1": {
					Type:   agentfactory.AgentTypeOpenAI,
					Model:  "gpt-4o",
					APIKey: "key",
				},
			},
			target: "o1",
		},
		{
			name: "exec_ok",
			config: agentfactory.FactoryConfig{
				"e1": {
					Type: agentfactory.AgentTypeExec,
					Cmd:  []string{"echo", "hello"},
				},
			},
			target: "e1",
		},
		{
			name: "not_found",
			config: agentfactory.FactoryConfig{
				"g1": {
					Type:  agentfactory.AgentTypeGeminiAIStudio,
					Model: "gemini-1.5-pro",
				},
			},
			target:  "other",
			wantErr: `agent "other" not found`,
		},
		{
			name: "unsupported_filtered",
			config: agentfactory.FactoryConfig{
				"u1": {Type: "unsupported"},
			},
			target:  "u1",
			wantErr: `agent "u1" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := agentfactory.NewFactory(tt.config)
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

func TestFactory_CreateAgent(t *testing.T) {
	config := agentfactory.FactoryConfig{
		"g1": {
			Type:   agentfactory.AgentTypeGeminiAIStudio,
			Model:  "gemini-1.5-pro",
			APIKey: "key",
		},
	}

	f := agentfactory.NewFactory(config)
	a, err := f.CreateAgent("g1", "test-agent", "desc", "instr")

	assert.NoError(t, err)
	assert.NotNil(t, a)
	assert.Equal(t, "test-agent", a.Name())
}
