package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/metalagman/norma/internal/logging"
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
	}
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(taskCmd())
	return rootCmd.Execute()
}

func initConfig() {
	path := cfgFile
	if path == "" {
		path = filepath.Join(".norma", "config.json")
	}
	viper.SetConfigFile(path)
	viper.SetConfigType("json")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
}
