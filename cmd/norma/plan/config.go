package plancmd

import (
	"fmt"
	"strings"

	"github.com/normahq/norma/internal/config"
	"github.com/spf13/viper"
)

type plannerRuntimeConfig struct {
	Runtime             config.Config
	PlannerProviderID   string
	CoordinatorAssignee string
}

func loadPlannerRuntimeConfig(workingDir string) (plannerRuntimeConfig, error) {
	loadOpts := config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	}

	runtimeCfg, plannerCfg, err := config.LoadRuntimeAndPlannerConfig(loadOpts)
	if err != nil {
		return plannerRuntimeConfig{}, err
	}
	plannerProviderID, err := runtimeCfg.ResolvePlannerProvider(plannerCfg)
	if err != nil {
		return plannerRuntimeConfig{}, err
	}

	_, swarmCfg, err := config.LoadRuntimeAndSwarmConfig(loadOpts)
	if err != nil {
		return plannerRuntimeConfig{}, err
	}
	swarmRoles, err := runtimeCfg.ResolveSwarmRoles(swarmCfg)
	if err != nil {
		return plannerRuntimeConfig{}, err
	}

	primaryRole := strings.TrimSpace(swarmCfg.PrimaryRole)
	primary, ok := swarmRoles[primaryRole]
	if !ok {
		return plannerRuntimeConfig{}, fmt.Errorf("swarm.primary_role %q does not exist in swarm.roles", primaryRole)
	}
	coordinatorAssignee := strings.TrimSpace(primary.Assignee)
	if coordinatorAssignee == "" {
		return plannerRuntimeConfig{}, fmt.Errorf("swarm.roles.%s.assignee is required", primaryRole)
	}

	return plannerRuntimeConfig{
		Runtime:             runtimeCfg,
		PlannerProviderID:   plannerProviderID,
		CoordinatorAssignee: coordinatorAssignee,
	}, nil
}
