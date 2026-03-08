package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestResolveConfigPath_DefaultYAMLPreferred(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), "profile: default\n"); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	got := resolveConfigPath(repoRoot, defaultConfigPath)
	want := filepath.Join(repoRoot, defaultConfigPath)
	if got != want {
		t.Fatalf("resolve config path = %q, want %q", got, want)
	}
}

func TestLoadConfig_UsesYAML(t *testing.T) {
	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: default
agents:
  opencode_exec_agent:
    type: opencode
    model: opencode/big-pickle
profiles:
  default:
    pdca:
      plan: opencode_exec_agent
      do: opencode_exec_agent
      check: opencode_exec_agent
      act: opencode_exec_agent
    planner: opencode_exec_agent
budgets:
  max_iterations: 1
retention:
  keep_last: 10
  keep_days: 5
`); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	cfg, err := loadConfig(repoRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Profile != "default" {
		t.Fatalf("profile = %q, want %q", cfg.Profile, "default")
	}
	if cfg.Budgets.MaxIterations != 1 {
		t.Fatalf("budgets.max_iterations = %d, want %d", cfg.Budgets.MaxIterations, 1)
	}
	if cfg.Agents["plan"].Type != "opencode" {
		t.Fatalf("plan agent type = %q, want %q", cfg.Agents["plan"].Type, "opencode")
	}
}

func TestLoadRawConfig_ExpandsEnvValues(t *testing.T) {
	repoRoot := t.TempDir()

	t.Setenv("NORMA_PROFILE", "default")
	t.Setenv("NORMA_AGENT_TYPE", "exec")
	t.Setenv("NORMA_AGENT_CMD", "ainvoke")
	t.Setenv("NORMA_MAX_ITERATIONS", "3")

	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: ${NORMA_PROFILE}
agents:
  local_exec:
    type: ${NORMA_AGENT_TYPE}
    cmd:
      - ${NORMA_AGENT_CMD}
profiles:
  default:
    pdca:
      plan: local_exec
      do: local_exec
      check: local_exec
      act: local_exec
budgets:
  max_iterations: ${NORMA_MAX_ITERATIONS}
`); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	cfg, err := loadRawConfig(repoRoot)
	if err != nil {
		t.Fatalf("load raw config: %v", err)
	}
	if cfg.Profile != "default" {
		t.Fatalf("profile = %q, want %q", cfg.Profile, "default")
	}
	if cfg.Budgets.MaxIterations != 3 {
		t.Fatalf("budgets.max_iterations = %d, want %d", cfg.Budgets.MaxIterations, 3)
	}
	agent := cfg.Agents["local_exec"]
	if agent.Type != "exec" {
		t.Fatalf("agents.local_exec.type = %q, want %q", agent.Type, "exec")
	}
	if len(agent.Cmd) != 1 || agent.Cmd[0] != "ainvoke" {
		t.Fatalf("agents.local_exec.cmd = %v, want %v", agent.Cmd, []string{"ainvoke"})
	}
}

func TestLoadConfig_ACPTypesAreSupported(t *testing.T) {
	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: acp
agents:
  gemini_acp_agent:
    type: gemini_acp
    model: gemini-3-flash-preview
  opencode_acp_agent:
    type: opencode_acp
    model: opencode/big-pickle
  codex_acp_agent:
    type: codex_acp
  custom_acp_agent:
    type: acp_exec
    cmd:
      - custom-acp
      - --stdio
profiles:
  acp:
    pdca:
      plan: gemini_acp_agent
      do: opencode_acp_agent
      check: codex_acp_agent
      act: custom_acp_agent
    planner: gemini_acp_agent
budgets:
  max_iterations: 2
`); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	cfg, err := loadConfig(repoRoot)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Profile != "acp" {
		t.Fatalf("profile = %q, want %q", cfg.Profile, "acp")
	}
	if cfg.Agents["plan"].Type != "gemini_acp" {
		t.Fatalf("plan agent type = %q, want %q", cfg.Agents["plan"].Type, "gemini_acp")
	}
	if cfg.Agents["do"].Type != "opencode_acp" {
		t.Fatalf("do agent type = %q, want %q", cfg.Agents["do"].Type, "opencode_acp")
	}
	if cfg.Agents["check"].Type != "codex_acp" {
		t.Fatalf("check agent type = %q, want %q", cfg.Agents["check"].Type, "codex_acp")
	}
	if cfg.Agents["act"].Type != "acp_exec" {
		t.Fatalf("act agent type = %q, want %q", cfg.Agents["act"].Type, "acp_exec")
	}
	if cfg.Agents["planner"].Type != "gemini_acp" {
		t.Fatalf("planner agent type = %q, want %q", cfg.Agents["planner"].Type, "gemini_acp")
	}
}

func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
