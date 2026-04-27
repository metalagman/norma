package plancmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadPlannerRuntimeConfig_LoadsPlannerProviderAndCoordinatorAssignee(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    planner-agent:
      type: generic_acp
      generic_acp:
        cmd: ["planner"]
    swarm-agent:
      type: generic_acp
      generic_acp:
        cmd: ["swarm"]
planner:
  provider: planner-agent
swarm:
  primary_role: coordinator
  default_provider: swarm-agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	cfg, err := loadPlannerRuntimeConfig(workingDir)
	if err != nil {
		t.Fatalf("loadPlannerRuntimeConfig() error = %v", err)
	}
	if got := cfg.PlannerProviderID; got != "planner-agent" {
		t.Fatalf("PlannerProviderID = %q, want planner-agent", got)
	}
	if got := cfg.CoordinatorAssignee; got != "norma-coordinator" {
		t.Fatalf("CoordinatorAssignee = %q, want norma-coordinator", got)
	}
}

func TestLoadPlannerRuntimeConfig_AppliesProfilePlannerOverride(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    base-agent:
      type: generic_acp
      generic_acp:
        cmd: ["base"]
    alt-agent:
      type: generic_acp
      generic_acp:
        cmd: ["alt"]
planner:
  provider: base-agent
swarm:
  primary_role: coordinator
  default_provider: base-agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
profiles:
  alt:
    planner:
      provider: alt-agent
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("profile", "alt")

	cfg, err := loadPlannerRuntimeConfig(workingDir)
	if err != nil {
		t.Fatalf("loadPlannerRuntimeConfig() error = %v", err)
	}
	if got := cfg.PlannerProviderID; got != "alt-agent" {
		t.Fatalf("PlannerProviderID = %q, want alt-agent", got)
	}
}

func TestLoadPlannerRuntimeConfig_ReturnsErrorForMissingPlannerProvider(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    swarm-agent:
      type: generic_acp
      generic_acp:
        cmd: ["swarm"]
swarm:
  primary_role: coordinator
  default_provider: swarm-agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, err := loadPlannerRuntimeConfig(workingDir)
	if err == nil {
		t.Fatal("loadPlannerRuntimeConfig() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "planner.provider is required") {
		t.Fatalf("error = %q, want missing planner.provider", err)
	}
}

func TestLoadPlannerRuntimeConfig_ReturnsErrorForUnknownPlannerProvider(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    swarm-agent:
      type: generic_acp
      generic_acp:
        cmd: ["swarm"]
planner:
  provider: missing
swarm:
  primary_role: coordinator
  default_provider: swarm-agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, err := loadPlannerRuntimeConfig(workingDir)
	if err == nil {
		t.Fatal("loadPlannerRuntimeConfig() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `planner.provider "missing" is not defined in runtime.providers`) {
		t.Fatalf("error = %q, want unknown planner.provider", err)
	}
}

func TestLoadPlannerRuntimeConfig_ReturnsErrorForMissingSwarmPrimaryRole(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    planner-agent:
      type: generic_acp
      generic_acp:
        cmd: ["planner"]
planner:
  provider: planner-agent
swarm:
  default_provider: planner-agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, err := loadPlannerRuntimeConfig(workingDir)
	if err == nil {
		t.Fatal("loadPlannerRuntimeConfig() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "swarm.primary_role is required") {
		t.Fatalf("error = %q, want missing swarm.primary_role", err)
	}
}

func TestLoadPlannerRuntimeConfig_ReturnsErrorForBlankCoordinatorAssignee(t *testing.T) {
	workingDir := t.TempDir()
	if err := writePlanConfigFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    planner-agent:
      type: generic_acp
      generic_acp:
        cmd: ["planner"]
planner:
  provider: planner-agent
swarm:
  primary_role: coordinator
  default_provider: planner-agent
  roles:
    coordinator:
      assignee: ""
      instruction: coordinate
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, err := loadPlannerRuntimeConfig(workingDir)
	if err == nil {
		t.Fatal("loadPlannerRuntimeConfig() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "swarm.roles.coordinator.assignee is required") {
		t.Fatalf("error = %q, want missing coordinator assignee", err)
	}
}

func writePlanConfigFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
