package swarmcmd

import (
	"github.com/normahq/norma/internal/config"
	"github.com/spf13/viper"
)

func loadRuntimeAndSwarmConfig(workingDir string) (config.Config, config.SwarmSettings, error) {
	return config.LoadRuntimeAndSwarmConfig(config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	})
}
