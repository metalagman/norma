package main

import (
	"os"
	"path/filepath"
	"testing"

	relayapp "github.com/normahq/norma/internal/apps/relay"
	"github.com/normahq/norma/pkg/runtime/appconfig"
)

type relayTestConfigDocument struct {
	Norma appconfig.NormaConfig `mapstructure:"norma"`
	Relay relayapp.RelayConfig  `mapstructure:"relay"`
}

func TestLoadConfigDocument_AppliesProfileRelayOverrides(t *testing.T) {
	workingDir := t.TempDir()
	t.Setenv("RELAY_TELEGRAM_WEBHOOK_ENABLED", "true")

	if err := writeFile(filepath.Join(workingDir, ".config", "relay", "config.yaml"), `norma:
  agents:
    relay_agent:
      type: opencode_acp
      opencode_acp:
        model: opencode/big-pickle
cli:
  pdca:
    plan: relay_agent
    do: relay_agent
    check: relay_agent
    act: relay_agent
  planner: relay_agent
profiles:
  default:
    relay:
      root_agent: relay_agent
`); err != nil {
		t.Fatalf("write relay config: %v", err)
	}

	var doc relayTestConfigDocument
	selectedProfile, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir, Profile: "default"},
		appconfig.AppLoadOptions{
			AppName:      "relay",
			DefaultsYAML: defaultRelayConfig,
		},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if selectedProfile != "default" {
		t.Fatalf("profile = %q, want default", selectedProfile)
	}

	relayCfg := relayapp.Config{Relay: doc.Relay}

	if relayCfg.Relay.RootAgent != "relay_agent" {
		t.Fatalf("root_agent = %q, want relay_agent", relayCfg.Relay.RootAgent)
	}
	if !relayCfg.Relay.Telegram.Webhook.Enabled {
		t.Fatal("webhook.enabled = false, want true from env override")
	}
}

func TestNewRootCommand_RegistersCommandsAndFlags(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-api-key")

	cmd, err := newRootCommand()
	if err != nil {
		t.Fatalf("newRootCommand: %v", err)
	}

	if _, _, err := cmd.Find([]string{"serve"}); err != nil {
		t.Fatalf("serve command missing: %v", err)
	}
	if _, _, err := cmd.Find([]string{"init"}); err != nil {
		t.Fatalf("init command missing: %v", err)
	}
	if _, _, err := cmd.Find([]string{"tool"}); err != nil {
		t.Fatalf("tool command missing: %v", err)
	}
	if _, _, err := cmd.Find([]string{"tool", "codex-acp-bridge"}); err != nil {
		t.Fatalf("tool codex-acp-bridge command missing: %v", err)
	}

	for _, name := range []string{"config-dir", "profile", "debug", "trace"} {
		if cmd.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("missing persistent flag %q", name)
		}
	}
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
