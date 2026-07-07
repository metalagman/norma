package main

import (
	"path/filepath"

	"github.com/normahq/norma/v2/internal/config"
	"github.com/spf13/viper"
)

var defaultConfigPath = filepath.Join(".norma", config.CoreConfigFileName)

func loadConfig(workingDir string) (config.Config, error) {
	return config.LoadRuntime(config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	})
}

func loadRuntimeAndCLIConfig(workingDir string) (config.Config, config.CLISettings, error) {
	return config.LoadRuntimeAndCLIConfig(config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	})
}
