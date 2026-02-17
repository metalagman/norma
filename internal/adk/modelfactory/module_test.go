package modelfactory_test

import (
	"testing"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/stretchr/testify/assert"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestModule(t *testing.T) {
	var factory *modelfactory.Factory

	app := fxtest.New(t,
		fx.Provide(func() modelfactory.FactoryConfig {
			return modelfactory.FactoryConfig{
				"gemini": {
					Type:   modelfactory.ModelTypeGeminiAIStudio,
					APIKey: "test-key",
				},
			}
		}),
		modelfactory.Module,
		fx.Populate(&factory),
	)
	defer app.RequireStart().RequireStop()

	assert.NotNil(t, factory)
	m, err := factory.CreateModel("gemini")
	assert.NoError(t, err)
	assert.NotNil(t, m)
}
