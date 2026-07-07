package main

import (
	"path/filepath"
	"testing"

	initcmd "github.com/normahq/norma/v2/cmd/norma/init"
	"github.com/normahq/norma/v2/internal/config"
	"github.com/spf13/viper"
)

func TestDefaultConfigYAML_IsLoadable(t *testing.T) {
	workingDir := t.TempDir()
	t.Setenv("GOOGLE_API_KEY", "test-google-api-key")
	if err := writeTestFile(filepath.Join(workingDir, defaultConfigPath), initcmd.DefaultConfigYAML); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	if _, err := loadConfig(workingDir); err != nil {
		t.Fatalf("load default config: %v", err)
	}
}

func TestDefaultConfigYAML_CodexProfileResolvesCodexAgent(t *testing.T) {
	const codexAgentID = "codex"

	workingDir := t.TempDir()
	t.Setenv("GOOGLE_API_KEY", "test-google-api-key")
	if err := writeTestFile(filepath.Join(workingDir, defaultConfigPath), initcmd.DefaultConfigYAML); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("profile", "codex")

	cfg, cliCfg, err := loadRuntimeAndCLIConfig(workingDir)
	if err != nil {
		t.Fatalf("load runtime and cli config: %v", err)
	}
	if cfg.Profile != "codex" {
		t.Fatalf("profile = %q, want codex", cfg.Profile)
	}
	for role, got := range cfg.RoleIDs {
		if got != codexAgentID {
			t.Fatalf("%s role id = %q, want %s", role, got, codexAgentID)
		}
	}
	if got := cliCfg.PDCA.Plan; got != codexAgentID {
		t.Fatalf("cli pdca plan = %q, want %s", got, codexAgentID)
	}

	loadOpts := config.RuntimeLoadOptions{WorkingDir: workingDir, Profile: "codex"}
	plannerRuntime, plannerCfg, err := config.LoadRuntimeAndPlannerConfig(loadOpts)
	if err != nil {
		t.Fatalf("load planner config: %v", err)
	}
	plannerProvider, err := plannerRuntime.ResolvePlannerProvider(plannerCfg)
	if err != nil {
		t.Fatalf("resolve planner provider: %v", err)
	}
	if plannerProvider != codexAgentID {
		t.Fatalf("planner provider = %q, want %s", plannerProvider, codexAgentID)
	}

	swarmRuntime, swarmCfg, err := config.LoadRuntimeAndSwarmConfig(loadOpts)
	if err != nil {
		t.Fatalf("load swarm config: %v", err)
	}
	swarmRoles, err := swarmRuntime.ResolveSwarmRoles(swarmCfg)
	if err != nil {
		t.Fatalf("resolve swarm roles: %v", err)
	}
	if got := swarmRoles["coordinator"].ProviderID; got != codexAgentID {
		t.Fatalf("coordinator provider = %q, want %s", got, codexAgentID)
	}
}
