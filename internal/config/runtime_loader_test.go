package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/normahq/norma/pkg/runtime/appconfig"
)

type relayConfigDocumentForTest struct {
	Norma appconfig.NormaConfig `mapstructure:"norma"`
	Relay struct {
		RootAgent               string            `mapstructure:"root_agent"`
		AgentSystemInstructions map[string]string `mapstructure:"agent_system_instructions"`
		Telegram                struct {
			Webhook struct {
				URL       string `mapstructure:"url"`
				Enabled   bool   `mapstructure:"enabled"`
				AuthToken string `mapstructure:"auth_token"`
			} `mapstructure:"webhook"`
		} `mapstructure:"telegram"`
		Logger struct {
			Level string `mapstructure:"level"`
		} `mapstructure:"logger"`
	} `mapstructure:"relay"`
}

func TestLoadRuntime_PrefersConfigDirOverRepoAndGlobal(t *testing.T) {
	workingDir := t.TempDir()
	xdgRoot := t.TempDir()
	extraRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	if err := writeRuntimeFile(filepath.Join(xdgRoot, "norma", "cli.yaml"), runtimeYAMLWithCmd("global")); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "cli.yaml"), runtimeYAMLWithCmd("repo")); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	if err := writeRuntimeFile(filepath.Join(extraRoot, "cli.yaml"), runtimeYAMLWithCmd("extra")); err != nil {
		t.Fatalf("write extra config: %v", err)
	}

	cfg, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir, ConfigDir: extraRoot})
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	agentCfg := cfg.Norma.Providers["agent"]
	if agentCfg.GenericACP == nil || len(agentCfg.GenericACP.Cmd) == 0 {
		t.Fatalf("agent generic_acp block missing cmd: %#v", agentCfg)
	}
	if got := agentCfg.GenericACP.Cmd[0]; got != "extra" {
		t.Fatalf("agent generic_acp.cmd[0] = %q, want extra", got)
	}
}

func TestLoadRuntime_UsesSingleEffectiveFileWithoutCrossRootMerge(t *testing.T) {
	workingDir := t.TempDir()
	xdgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgRoot)

	if err := writeRuntimeFile(filepath.Join(xdgRoot, "norma", "cli.yaml"), runtimeYAMLWithCmd("global")); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "cli.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["repo-only"]
`); err != nil {
		t.Fatalf("write repo config: %v", err)
	}

	_, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir})
	if err == nil {
		t.Fatal("LoadRuntime returned nil error, want validation error from incomplete repo config")
	}
}

func TestLoadConfigDocument_AppliesProfileOverridesAndEnv(t *testing.T) {
	workingDir := t.TempDir()
	t.Setenv("RELAY_TELEGRAM_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("RELAY_TELEGRAM_WEBHOOK_ENABLED", "true")
	t.Setenv("RELAY_TELEGRAM_WEBHOOK_AUTH_TOKEN", "auth-token")

	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "relay", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["agent"]
relay:
  root_agent: from_relay_file
profiles:
  default:
    relay:
      logger:
        level: debug
      agent_system_instructions:
        agent: relay-override
`); err != nil {
		t.Fatalf("write relay config: %v", err)
	}

	var doc relayConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir, Profile: "default"},
		appconfig.AppLoadOptions{
			AppName: "relay",
			DefaultsYAML: []byte(`relay:
  telegram:
    webhook:
      url: ""
      enabled: false
      auth_token: ""
`),
		},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}

	if got := doc.Relay.RootAgent; got != "from_relay_file" {
		t.Fatalf("root_agent = %q, want from_relay_file", got)
	}
	if got := doc.Relay.Telegram.Webhook.URL; got != "https://example.com/webhook" {
		t.Fatalf("telegram.webhook.url = %q, want https://example.com/webhook", got)
	}
	if !doc.Relay.Telegram.Webhook.Enabled {
		t.Fatalf("telegram.webhook.enabled = false, want true from env")
	}
	if got := doc.Relay.Telegram.Webhook.AuthToken; got != "auth-token" {
		t.Fatalf("telegram.webhook.auth_token = %q, want auth-token from env", got)
	}
	if got := doc.Relay.Logger.Level; got != "debug" {
		t.Fatalf("logger.level = %q, want debug", got)
	}
	if got := doc.Relay.AgentSystemInstructions["agent"]; got != "relay-override" {
		t.Fatalf("relay.agent_system_instructions[agent] = %q, want relay-override", got)
	}
}

func TestLoadConfigDocument_UsesDedicatedRelayConfigPathWithoutLegacyMerge(t *testing.T) {
	workingDir := t.TempDir()

	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["core-agent"]
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
relay:
  root_agent: from_core_file
  telegram:
    webhook:
      url: https://legacy.example/webhook
`); err != nil {
		t.Fatalf("write core config: %v", err)
	}
	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "relay", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["relay-agent"]
relay:
  root_agent: from_dedicated_relay_config
`); err != nil {
		t.Fatalf("write relay config: %v", err)
	}

	var doc relayConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{AppName: "relay"},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}

	if got := doc.Relay.RootAgent; got != "from_dedicated_relay_config" {
		t.Fatalf("root_agent = %q, want from_dedicated_relay_config", got)
	}
	if got := doc.Relay.Telegram.Webhook.URL; got != "" {
		t.Fatalf("relay.telegram.webhook unexpectedly loaded from legacy core config")
	}
}

func TestLoadConfigDocument_DoesNotFallbackToLegacyCoreWhenDedicatedRelayConfigMissing(t *testing.T) {
	workingDir := t.TempDir()

	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["core-agent"]
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
relay:
  root_agent: from_core_file
`); err != nil {
		t.Fatalf("write core config: %v", err)
	}

	var doc relayConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{AppName: "relay"},
		&doc,
	)
	if err == nil {
		t.Fatal("LoadConfigDocument returned nil error, want config not found for dedicated relay path")
	}
}

func TestLoadRuntime_AcceptsNormaMCPServersKey(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["agent"]
  mcp_servers:
    tasks:
      type: stdio
      cmd: ["norma", "mcp", "tasks"]
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
`); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	cfg, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir})
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	if len(cfg.Norma.MCPServers) != 1 {
		t.Fatalf("len(cfg.Norma.MCPServers) = %d, want 1", len(cfg.Norma.MCPServers))
	}
}

func TestLoadRuntime_AllowsExtraOutOfScopeFields(t *testing.T) {
	workingDir := t.TempDir()
	content := "norma:\n" +
		"  providers:\n" +
		"    agent:\n" +
		"      type: generic_acp\n" +
		"      generic_acp:\n" +
		"        cmd: [\"agent\"]\n" +
		"        api_key: \"secret\"\n" +
		"cli:\n" +
		"  pdca:\n" +
		"    plan: agent\n" +
		"    do: agent\n" +
		"    check: agent\n" +
		"    act: agent\n"
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), content); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	if _, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir}); err != nil {
		t.Fatalf("LoadRuntime returned error for extra field: %v", err)
	}
}

func TestLoadRuntime_IgnoresLegacyTopLevelRuntimeKeys(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `providers:
  legacy_agent:
    type: generic_acp
    generic_acp:
      cmd: ["legacy"]
mcp_servers:
  legacy:
    type: stdio
    cmd: ["legacy-mcp"]
norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["current"]
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
`); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	cfg, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir})
	if err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
	agentCfg := cfg.Norma.Providers["agent"]
	if agentCfg.GenericACP == nil || len(agentCfg.GenericACP.Cmd) == 0 {
		t.Fatalf("agent generic_acp block missing cmd: %#v", agentCfg)
	}
	if got := agentCfg.GenericACP.Cmd[0]; got != "current" {
		t.Fatalf("agent generic_acp.cmd[0] = %q, want current", got)
	}
}

func TestLoadRuntime_IgnoresLegacyNormaKeys(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["current"]
  mcps:
    old:
      type: stdio
      cmd: ["old"]
  profiles:
    old:
      pdca:
        plan: old
  budgets:
    max_iterations: 1
  retention:
    keep_last: 1
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
`); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	if _, err := LoadRuntime(RuntimeLoadOptions{WorkingDir: workingDir}); err != nil {
		t.Fatalf("LoadRuntime returned error: %v", err)
	}
}

func TestLoadConfigDocument_IgnoresLegacyKeysInProfileOverride(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "relay", "config.yaml"), `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["agent"]
relay:
  root_agent: from_relay_file
profiles:
  default:
    providers:
      legacy_agent:
        type: generic_acp
        generic_acp:
          cmd: ["legacy"]
    pdca:
      plan: legacy_agent
    relay:
      logger:
        level: debug
`); err != nil {
		t.Fatalf("write relay config: %v", err)
	}

	var doc relayConfigDocumentForTest
	selectedProfile, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir, Profile: "default"},
		appconfig.AppLoadOptions{AppName: "relay"},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if selectedProfile != "default" {
		t.Fatalf("profile = %q, want default", selectedProfile)
	}
	if got := doc.Relay.Logger.Level; got != "debug" {
		t.Fatalf("logger.level = %q, want debug", got)
	}
}

func runtimeYAMLWithCmd(cmd string) string {
	return `norma:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["` + cmd + `"]
cli:
  pdca:
    plan: agent
    do: agent
    check: agent
    act: agent
`
}

func writeRuntimeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
