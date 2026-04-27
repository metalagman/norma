package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfig_LoadsRuntimeFromConfigYAML(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeTestFile(filepath.Join(workingDir, defaultConfigPath), `runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
cli:
  pdca:
    plan: opencode
    do: opencode
    check: opencode
    act: opencode
  budgets:
    max_iterations: 7
planner:
  provider: opencode
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	cfg, err := loadConfig(workingDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Profile != "default" {
		t.Fatalf("profile = %q, want default", cfg.Profile)
	}
	if cfg.RoleIDs["plan"] != "opencode" {
		t.Fatalf("plan role id = %q, want opencode", cfg.RoleIDs["plan"])
	}
	plan := cfg.Runtime.Providers[cfg.RoleIDs["plan"]]
	if plan.Type != "opencode_acp" {
		t.Fatalf("plan agent type = %q, want opencode_acp", plan.Type)
	}
	if plan.OpenCodeACP == nil || plan.OpenCodeACP.Model != "opencode/big-pickle" {
		t.Fatalf("plan opencode_acp block = %#v, want model opencode/big-pickle", plan.OpenCodeACP)
	}
}

func TestLoadRuntimeAndCLIConfig_LoadsCLIAppSettings(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeTestFile(filepath.Join(workingDir, defaultConfigPath), `runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
cli:
  pdca:
    plan: opencode
    do: opencode
    check: opencode
    act: opencode
  budgets:
    max_iterations: 9
  retention:
    keep_last: 15
    keep_days: 8
`); err != nil {
		t.Fatalf("write config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, cliCfg, err := loadRuntimeAndCLIConfig(workingDir)
	if err != nil {
		t.Fatalf("load runtime and cli config: %v", err)
	}
	if got := cliCfg.EffectiveBudgets().MaxIterations; got != 9 {
		t.Fatalf("effective max_iterations = %d, want 9", got)
	}
	ret := cliCfg.EffectiveRetention()
	if ret.KeepLast != 15 || ret.KeepDays != 8 {
		t.Fatalf("retention = %+v, want keep_last=15 keep_days=8", ret)
	}
}

func TestLoadConfig_IgnoresNormaYAML(t *testing.T) {
	workingDir := t.TempDir()
	normaYAMLPath := filepath.Join(workingDir, ".norma", "norma.yaml")
	if err := writeTestFile(normaYAMLPath, `runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
cli:
  pdca:
    plan: opencode
    do: opencode
    check: opencode
    act: opencode
`); err != nil {
		t.Fatalf("write norma.yaml: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)

	_, err := loadConfig(workingDir)
	if err == nil {
		t.Fatal("loadConfig returned nil error, want missing cli.yaml/config.yaml error")
	}
	if !strings.Contains(err.Error(), ".norma/cli.yaml") || !strings.Contains(err.Error(), ".norma/config.yaml") {
		t.Fatalf("error = %q, want mention of .norma/cli.yaml and .norma/config.yaml", err)
	}
}

func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
