package execmodel_test

import (
	"testing"

	"github.com/metalagman/ainvoke"
	"github.com/metalagman/norma/internal/adk/execmodel"
	"github.com/stretchr/testify/assert"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestModule(t *testing.T) {
	var m *execmodel.Model

	app := fxtest.New(t,
		fx.Provide(func() execmodel.Config {
			return execmodel.Config{
				AgentConfig: ainvoke.AgentConfig{
					Cmd: []string{"echo"},
				},
			}
		}),
		execmodel.Module,
		fx.Populate(&m),
	)
	defer app.RequireStart().RequireStop()

	assert.NotNil(t, m)
	assert.Equal(t, "echo", m.Name())
}
