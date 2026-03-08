package main

import (
	"path/filepath"
	"testing"

	initcmd "github.com/metalagman/norma/cmd/norma/init"
	"github.com/spf13/viper"
)

func TestDefaultConfigYAML_IsLoadable(t *testing.T) {
	repoRoot := t.TempDir()
	t.Setenv("GOOGLE_API_KEY", "test-google-api-key")
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), initcmd.DefaultConfigYAML); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	if _, err := loadConfig(repoRoot); err != nil {
		t.Fatalf("load default config: %v", err)
	}
}
