package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDotEnv_LoadsFromCurrentWorkingDirectory(t *testing.T) {
	workingDir := setWorkingDir(t)
	unsetEnvForTest(t, "RELAY_TELEGRAM_TOKEN")

	if err := writeFile(filepath.Join(workingDir, ".env"), "RELAY_TELEGRAM_TOKEN=from-cwd-dotenv\n"); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	initDotEnv()

	if got := os.Getenv("RELAY_TELEGRAM_TOKEN"); got != "from-cwd-dotenv" {
		t.Fatalf("RELAY_TELEGRAM_TOKEN = %q, want %q", got, "from-cwd-dotenv")
	}
}

func TestInitDotEnv_DoesNotLoadFromDotConfigRelay(t *testing.T) {
	workingDir := setWorkingDir(t)
	unsetEnvForTest(t, "RELAY_TELEGRAM_TOKEN")

	if err := writeFile(filepath.Join(workingDir, ".config", "relay", ".env"), "RELAY_TELEGRAM_TOKEN=from-config-relay-dotenv\n"); err != nil {
		t.Fatalf("write .config/relay/.env: %v", err)
	}

	initDotEnv()

	if got := os.Getenv("RELAY_TELEGRAM_TOKEN"); got != "" {
		t.Fatalf("RELAY_TELEGRAM_TOKEN = %q, want empty (CWD .env only)", got)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	prevValue, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if !wasSet {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, prevValue)
	})
}
