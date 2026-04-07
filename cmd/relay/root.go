package main

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/joho/godotenv"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configDir string
	debug     bool
	trace     bool
	profile   string
)

// Execute runs the relay root command.
func Execute() error {
	cmd, err := newRootCommand()
	if err != nil {
		return err
	}
	return cmd.Execute()
}

func newRootCommand() (*cobra.Command, error) {
	cobra.OnInitialize(initDotEnv)

	cmd := &cobra.Command{
		Use:   "relay",
		Short: "relay is a standalone Telegram relay server for norma",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			logLevel := logging.LevelInfo
			if debug {
				logLevel = logging.LevelDebug
			}
			if trace {
				logLevel = logging.LevelTrace
			}
			return logging.Init(logging.WithLevel(logLevel))
		},
	}

	cmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "extra config root directory (highest priority)")
	cmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")
	cmd.PersistentFlags().BoolVar(&trace, "trace", false, "enable trace logging (overrides --debug)")
	cmd.PersistentFlags().StringVar(&profile, "profile", "", "config profile name")

	if err := viper.BindPFlag("config_dir", cmd.PersistentFlags().Lookup("config-dir")); err != nil {
		return nil, fmt.Errorf("bind config-dir flag: %w", err)
	}
	if err := viper.BindPFlag("profile", cmd.PersistentFlags().Lookup("profile")); err != nil {
		return nil, fmt.Errorf("bind profile flag: %w", err)
	}

	cmd.AddCommand(serveCommand())
	cmd.AddCommand(initCommand())
	cmd.AddCommand(toolCommand())
	return cmd, nil
}

func initDotEnv() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn().Err(err).Msg("failed to load .env file")
	}
}
