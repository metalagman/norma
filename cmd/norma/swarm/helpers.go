package swarmcmd

import (
	"fmt"

	"github.com/normahq/norma/internal/config"
	"github.com/spf13/viper"
)

func loadRuntimeAndCLIConfigUnresolved(workingDir string) (config.Config, config.CLISettings, error) {
	cfg, cliCfg, err := config.LoadRuntimeAndCLIConfigUnresolved(config.RuntimeLoadOptions{
		WorkingDir: workingDir,
		ConfigDir:  viper.GetString("config_dir"),
		Profile:    viper.GetString("profile"),
	})
	if err != nil {
		return config.Config{}, config.CLISettings{}, err
	}
	if cliCfg.EffectiveBudgets().MaxIterations <= 0 {
		return config.Config{}, config.CLISettings{}, fmt.Errorf("cli.budgets.max_iterations must be > 0")
	}
	return cfg, cliCfg, nil
}
