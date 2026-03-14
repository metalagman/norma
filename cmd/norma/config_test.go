package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

const (
	genericACPType = "generic_acp"
	acpType        = "acp"
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
  opencode_acp_agent:
    type: opencode_acp
    model: opencode/big-pickle
profiles:
  default:
    pdca:
      plan: opencode_acp_agent
      do: opencode_acp_agent
      check: opencode_acp_agent
      act: opencode_acp_agent
    planner: opencode_acp_agent
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
	planAgentID, ok := cfg.RoleIDs["plan"]
	if !ok {
		t.Fatal("plan agent ID not found in RoleIDs")
	}
	planAgent := cfg.Agents[planAgentID]
	if planAgent.Type != genericACPType {
		t.Fatalf("plan agent type = %q, want %q", planAgent.Type, genericACPType)
	}
	if gotCmd := planAgent.Cmd; len(gotCmd) < 2 || gotCmd[0] != "opencode" || gotCmd[1] != acpType {
		t.Fatalf("plan agent cmd = %v, want opencode acp command", gotCmd)
	}
}

func TestLoadRawConfig_ExpandsEnvValues(t *testing.T) {
	repoRoot := t.TempDir()

	t.Setenv("NORMA_PROFILE", "default")
	t.Setenv("NORMA_AGENT_TYPE", "generic_acp")
	t.Setenv("NORMA_AGENT_CMD", "custom-acp")
	t.Setenv("NORMA_MAX_ITERATIONS", "3")

	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: ${NORMA_PROFILE}
agents:
  local_acp:
    type: ${NORMA_AGENT_TYPE}
    cmd:
      - ${NORMA_AGENT_CMD}
profiles:
  default:
    pdca:
      plan: local_acp
      do: local_acp
      check: local_acp
      act: local_acp
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
	// Note: loadRawConfig does not resolve RoleIDs, it only expanded env vars and validated schema.
	// But it does normalize agent aliases.
	agent := cfg.Agents["local_acp"]
	if agent.Type != genericACPType {
		t.Fatalf("agents.local_acp.type = %q, want %q", agent.Type, genericACPType)
	}
	if len(agent.Cmd) != 1 || agent.Cmd[0] != "custom-acp" {
		t.Fatalf("agents.local_acp.cmd = %v, want %v", agent.Cmd, []string{"custom-acp"})
	}
}

func TestLoadConfig_ACPTypesAreSupported(t *testing.T) {
	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: acp
agents:
  gemini_acp_agent:
    type: gemini_acp
    model: gemini-3-flash-preview
    mode: code
  opencode_acp_agent:
    type: opencode_acp
    model: opencode/big-pickle
  codex_acp_agent:
    type: codex_acp
  copilot_acp_agent:
    type: copilot_acp
  custom_acp_agent:
    type: generic_acp
    cmd:
      - custom-acp
      - --stdio
profiles:
  acp:
    pdca:
      plan: gemini_acp_agent
      do: opencode_acp_agent
      check: codex_acp_agent
      act: copilot_acp_agent
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
	if cfg.Profile != acpType {
		t.Fatalf("profile = %q, want %q", cfg.Profile, acpType)
	}

	checkRole := func(role, wantID, wantType string) {
		t.Helper()
		id, ok := cfg.RoleIDs[role]
		if !ok {
			t.Fatalf("%s agent ID not found in RoleIDs", role)
		}
		if id != wantID {
			t.Fatalf("%s agent ID = %q, want %q", role, id, wantID)
		}
		agent := cfg.Agents[id]
		if agent.Type != wantType {
			t.Fatalf("%s agent type = %q, want %q", role, agent.Type, wantType)
		}
	}

	checkRole("plan", "gemini_acp_agent", genericACPType)
	checkRole("do", "opencode_acp_agent", genericACPType)
	checkRole("check", "codex_acp_agent", genericACPType)
	checkRole("act", "copilot_acp_agent", genericACPType)
	checkRole("planner", "gemini_acp_agent", genericACPType)

	planAgent := cfg.Agents[cfg.RoleIDs["plan"]]
	planCmd := planAgent.Cmd
	if len(planCmd) < 4 || planCmd[0] != "gemini" || planCmd[1] != "--experimental-acp" || planCmd[2] != "--model" || planCmd[3] != "gemini-3-flash-preview" {
		t.Fatalf("plan agent cmd = %v, want gemini ACP command with model", planCmd)
	}
	if planAgent.Mode != "code" {
		t.Fatalf("plan agent mode = %q, want %q", planAgent.Mode, "code")
	}
	doAgent := cfg.Agents[cfg.RoleIDs["do"]]
	doCmd := doAgent.Cmd
	if len(doCmd) < 2 || doCmd[0] != "opencode" || doCmd[1] != acpType {
		t.Fatalf("do agent cmd = %v, want opencode acp command", doCmd)
	}
	checkAgent := cfg.Agents[cfg.RoleIDs["check"]]
	checkCmd := checkAgent.Cmd
	if len(checkCmd) < 3 || checkCmd[1] != "tool" || checkCmd[2] != "codex-acp-bridge" {
		t.Fatalf("check agent cmd = %v, want codex tool command", checkCmd)
	}
	actAgent := cfg.Agents[cfg.RoleIDs["act"]]
	actCmd := actAgent.Cmd
	if len(actCmd) < 2 || actCmd[0] != "copilot" || actCmd[1] != "--acp" {
		t.Fatalf("act agent cmd = %v, want copilot --acp command", actCmd)
	}
}

func TestLoadConfig_RejectExecTypes(t *testing.T) {
	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), `profile: default
agents:
  exec_agent:
    type: generic_exec
    cmd:
      - custom-exec
profiles:
  default:
    pdca:
      plan: exec_agent
      do: exec_agent
      check: exec_agent
      act: exec_agent
budgets:
  max_iterations: 1
`); err != nil {
		t.Fatalf("write yaml config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	_, err := loadConfig(repoRoot)
	if err == nil {
		t.Fatal("loadConfig returned nil error, want rejection of generic_exec")
	}
}

func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
