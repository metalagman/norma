package appconfig

import (
	"testing"
	"time"
)

func TestLoadResolvedSettingsUsesDefaultsWhenConfigFileMissing(t *testing.T) {
	defaults := []byte(`
assistant:
  telegram:
    token: default-token
`)

	settings, selectedProfile, err := LoadResolvedSettings(
		RuntimeLoadOptions{WorkingDir: t.TempDir()},
		AppLoadOptions{
			AppName:            "assistant",
			DefaultsYAML:       defaults,
			UseDotConfigAppDir: true,
		},
	)
	if err != nil {
		t.Fatalf("LoadResolvedSettings() error = %v", err)
	}
	if selectedProfile != "default" {
		t.Fatalf("selected profile = %q, want default", selectedProfile)
	}

	assistantSettings, ok := extractAppSection(settings, "assistant")
	if !ok {
		t.Fatal("assistant settings not found")
	}
	telegramSettings, ok := toStringAnyMap(assistantSettings["telegram"])
	if !ok {
		t.Fatal("assistant.telegram settings not found")
	}
	if got := telegramSettings["token"]; got != "default-token" {
		t.Fatalf("assistant.telegram.token = %q, want default-token", got)
	}
}

func TestLoadResolvedSettingsAppliesExplicitEnvPrefix(t *testing.T) {
	t.Setenv("APP_TELEGRAM_TOKEN", "env-token")
	t.Setenv("ASSISTANT_TELEGRAM_TOKEN", "wrong-token")

	defaults := []byte(`
assistant:
  telegram:
    token: default-token
`)

	settings, _, err := LoadResolvedSettings(
		RuntimeLoadOptions{WorkingDir: t.TempDir()},
		AppLoadOptions{
			AppName:            "assistant",
			EnvPrefix:          "APP",
			DefaultsYAML:       defaults,
			UseDotConfigAppDir: true,
		},
	)
	if err != nil {
		t.Fatalf("LoadResolvedSettings() error = %v", err)
	}

	assistantSettings, ok := extractAppSection(settings, "assistant")
	if !ok {
		t.Fatal("assistant settings not found")
	}
	telegramSettings, ok := toStringAnyMap(assistantSettings["telegram"])
	if !ok {
		t.Fatal("assistant.telegram settings not found")
	}
	if got := telegramSettings["token"]; got != "env-token" {
		t.Fatalf("assistant.telegram.token = %q, want env-token", got)
	}
}

func TestDecodeSettingsDecodesDurationStrings(t *testing.T) {
	type document struct {
		Telegram struct {
			Webhook struct {
				RegisterTimeout time.Duration `mapstructure:"register_timeout"`
			} `mapstructure:"webhook"`
		} `mapstructure:"telegram"`
	}

	var doc document
	if err := DecodeSettings(map[string]any{
		"telegram": map[string]any{
			"webhook": map[string]any{
				"register_timeout": "45s",
			},
		},
	}, &doc); err != nil {
		t.Fatalf("DecodeSettings() error = %v", err)
	}
	if doc.Telegram.Webhook.RegisterTimeout != 45*time.Second {
		t.Fatalf("register timeout = %s, want 45s", doc.Telegram.Webhook.RegisterTimeout)
	}
}
