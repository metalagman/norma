package main

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultConfigYAML_IsLoadable(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	if err := writeTestFile(filepath.Join(repoRoot, defaultConfigPath), defaultConfigYAML); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("config", defaultConfigPath)

	if _, err := loadConfig(repoRoot); err != nil {
		t.Fatalf("load default config: %v", err)
	}
}
