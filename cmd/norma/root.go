package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/metalagman/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	debug   bool
	rootCmd = &cobra.Command{
		Use:   "norma",
		Short: "norma is a minimal agent workflow runner",
	}
)

// Execute runs the root command.
func Execute() error {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", filepath.Join(".norma", "config.json"), "config file path")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")
	if err := viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config")); err != nil {
		return fmt.Errorf("bind config flag: %w", err)
	}
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		logging.Init(debug)
		if err := initBeads(); err != nil {
			log.Warn().Err(err).Msg("failed to initialize beads")
		}
	}
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(runsCmd())
	rootCmd.AddCommand(taskCmd())
	rootCmd.AddCommand(uiCmd())
	return rootCmd.Execute()
}

func initBeads() error {
	if _, err := os.Stat(".beads"); err == nil {
		return nil
	}

	log.Info().Msg(".beads not found, initializing with prefix 'norma'")
	cmd := exec.Command("bd", "init", "--prefix", "norma")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func initConfig() {
	path := cfgFile
	if path == "" {
		path = filepath.Join(".norma", "config.json")
	}
	viper.SetConfigFile(path)
	viper.SetConfigType("json")
}
