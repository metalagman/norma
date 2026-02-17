package planner

import (
	"fmt"

	"github.com/metalagman/norma/internal/adk/modelfactory"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/task"
	"go.uber.org/fx"
	"google.golang.org/adk/model"
)

// Module is the Fx module for the planner.
var Module = fx.Module("planner",
	fx.Provide(
		func(tracker *task.BeadsTracker) *BeadsTool {
			return newBeadsTool(tracker)
		},
		NewLLMPlanner,
		providePlannerModel,
	),
)

func providePlannerModel(
	f *modelfactory.Factory,
	cfg config.Config,
) (model.LLM, error) {
	// We need to resolve which agent is the "planner"
	_, profileCfg, err := cfg.ResolveProfile("")
	if err != nil {
		return nil, fmt.Errorf("resolve profile: %w", err)
	}

	if profileCfg.Planner == "" {
		return nil, fmt.Errorf("planner agent not configured in profile")
	}

	return f.CreateModel(profileCfg.Planner)
}

// ToFactoryConfig converts config.Config agents to modelfactory.FactoryConfig.
func ToFactoryConfig(cfg config.Config) modelfactory.FactoryConfig {
	return modelfactory.FactoryConfig(cfg.Agents)
}
