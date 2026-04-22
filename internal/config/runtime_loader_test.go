package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/normahq/norma/pkg/runtime/appconfig"
)

type appConfigDocumentForTest struct {
	Runtime appconfig.RuntimeConfig `mapstructure:"runtime"`
	App     struct {
		RootAgent          string `mapstructure:"root_agent"`
		SystemInstructions string `mapstructure:"system_instructions"`
		Telegram           struct {
			Webhook struct {
				URL       string `mapstructure:"url"`
				Enabled   bool   `mapstructure:"enabled"`
				AuthToken string `mapstructure:"auth_token"`
			} `mapstructure:"webhook"`
		} `mapstructure:"telegram"`
		Logger struct {
			Level string `mapstructure:"level"`
		} `mapstructure:"logger"`
	} `mapstructure:"app"`
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
	agentCfg := cfg.Runtime.Providers["agent"]
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
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "cli.yaml"), `runtime:
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
	t.Setenv("APP_TELEGRAM_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("APP_TELEGRAM_WEBHOOK_ENABLED", "true")
	t.Setenv("APP_TELEGRAM_WEBHOOK_AUTH_TOKEN", "auth-token")

	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "app", "config.yaml"), `runtime:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["agent"]
app:
  root_agent: from_app_file
profiles:
  default:
    app:
      logger:
        level: debug
      system_instructions: app-override
`); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	var doc appConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir, Profile: "default"},
		appconfig.AppLoadOptions{
			AppName:            "app",
			UseDotConfigAppDir: true,
			DefaultsYAML: []byte(`app:
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

	if got := doc.App.RootAgent; got != "from_app_file" {
		t.Fatalf("root_agent = %q, want from_app_file", got)
	}
	if got := doc.App.Telegram.Webhook.URL; got != "https://example.com/webhook" {
		t.Fatalf("telegram.webhook.url = %q, want https://example.com/webhook", got)
	}
	if !doc.App.Telegram.Webhook.Enabled {
		t.Fatalf("telegram.webhook.enabled = false, want true from env")
	}
	if got := doc.App.Telegram.Webhook.AuthToken; got != "auth-token" {
		t.Fatalf("telegram.webhook.auth_token = %q, want auth-token from env", got)
	}
	if got := doc.App.Logger.Level; got != "debug" {
		t.Fatalf("logger.level = %q, want debug", got)
	}
	if got := doc.App.SystemInstructions; got != "app-override" {
		t.Fatalf("app.system_instructions = %q, want app-override", got)
	}
}

func TestLoadConfigDocument_UsesDedicatedAppConfigPathWithoutLegacyMerge(t *testing.T) {
	workingDir := t.TempDir()

	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
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
app:
  root_agent: from_core_file
  telegram:
    webhook:
      url: https://legacy.example/webhook
`); err != nil {
		t.Fatalf("write core config: %v", err)
	}
	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "app", "config.yaml"), `runtime:
  providers:
    agent:
      type: generic_acp
      generic_acp:
        cmd: ["app-agent"]
app:
  root_agent: from_dedicated_app_config
`); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	var doc appConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{AppName: "app", UseDotConfigAppDir: true},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}

	if got := doc.App.RootAgent; got != "from_dedicated_app_config" {
		t.Fatalf("root_agent = %q, want from_dedicated_app_config", got)
	}
	if got := doc.App.Telegram.Webhook.URL; got != "" {
		t.Fatalf("app.telegram.webhook unexpectedly loaded from legacy core config")
	}
}

func TestLoadConfigDocument_DoesNotFallbackToLegacyCoreWhenDedicatedAppConfigMissing(t *testing.T) {
	workingDir := t.TempDir()

	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
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
app:
  root_agent: from_core_file
`); err != nil {
		t.Fatalf("write core config: %v", err)
	}

	var doc appConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{AppName: "app", UseDotConfigAppDir: true},
		&doc,
	)
	if err == nil {
		t.Fatal("LoadConfigDocument returned nil error, want config not found for dedicated app path")
	}
}

func TestLoadRuntime_AcceptsNormaMCPServersKey(t *testing.T) {
	workingDir := t.TempDir()
	if err := writeRuntimeFile(filepath.Join(workingDir, ".norma", "config.yaml"), `runtime:
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
	if len(cfg.Runtime.MCPServers) != 1 {
		t.Fatalf("len(cfg.Runtime.MCPServers) = %d, want 1", len(cfg.Runtime.MCPServers))
	}
}

func TestLoadRuntime_AllowsExtraOutOfScopeFields(t *testing.T) {
	workingDir := t.TempDir()
	content := "runtime:\n" +
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

func TestLoadConfigDocument_AppliesEnvOverridesToRuntimeSection(t *testing.T) {
	workingDir := t.TempDir()
	t.Setenv("APP_PROVIDERS_OPENCODE_OPENCODE_ACP_MODEL", "env-override-model")

	if err := writeRuntimeFile(filepath.Join(workingDir, ".config", "app", "config.yaml"), `runtime:
  providers:
    opencode:
      type: opencode_acp
      opencode_acp:
        model: file-model
app:
  root_agent: opencode
`); err != nil {
		t.Fatalf("write app config: %v", err)
	}

	var doc appConfigDocumentForTest
	_, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{AppName: "app", UseDotConfigAppDir: true},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}

	if got := doc.Runtime.Providers["opencode"].OpenCodeACP.Model; got != "env-override-model" {
		t.Fatalf("runtime.providers.opencode.opencode_acp.model = %q, want env-override-model", got)
	}
}

func runtimeYAMLWithCmd(cmd string) string {
	return `runtime:
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
