package agentfactory_test

import (
	"testing"

	"github.com/metalagman/norma/internal/agentfactory"
	"github.com/stretchr/testify/assert"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestModule(t *testing.T) {
	var factory *agentfactory.Factory

	app := fxtest.New(t,
		fx.Provide(func() agentfactory.FactoryConfig {
			return agentfactory.FactoryConfig{
				"gemini": {
					Type:   agentfactory.AgentTypeGeminiAIStudio,
					APIKey: "test-key",
				},
			}
		}),
		agentfactory.Module,
		fx.Populate(&factory),
	)
	defer app.RequireStart().RequireStop()

	assert.NotNil(t, factory)
	m, err := factory.CreateModel("gemini")
	assert.NoError(t, err)
	assert.NotNil(t, m)
}
