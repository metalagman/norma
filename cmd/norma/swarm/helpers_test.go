package swarmcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadRuntimeAndSwarmConfig_LoadsSwarmWithoutCLISettings(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeTestFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
  providers:
    agent:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
swarm:
  primary_role: coordinator
  default_provider: agent
  roles:
    coordinator:
      assignee: norma-coordinator
      instruction: coordinate
cli:
  budgets:
    max_iterations: 0
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, swarmCfg, err := loadRuntimeAndSwarmConfig(workingDir)
	if err != nil {
		t.Fatalf("load runtime and swarm config: %v", err)
	}
	if got := swarmCfg.PrimaryRole; got != "coordinator" {
		t.Fatalf("primary_role = %q, want coordinator", got)
	}
}

func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
