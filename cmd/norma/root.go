// Package main provides the entry point for the norma CLI.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	debug   bool
	profile string
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
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "config profile name")
	if err := viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config")); err != nil {
		return fmt.Errorf("bind config flag: %w", err)
	}
	if err := viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile")); err != nil {
		return fmt.Errorf("bind profile flag: %w", err)
	}
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		logging.Init(debug)
		repoRoot, err := os.Getwd()
		if err != nil {
			log.Warn().Err(err).Msg("failed to get current working directory")
			return
		}
		if git.Available(cmd.Context(), repoRoot) {
			if err := initBeads(); err != nil {
				log.Warn().Err(err).Msg("failed to initialize beads")
			}
		}
	}
	rootCmd.AddCommand(loopCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(runsCmd())
	rootCmd.AddCommand(taskCmd())
	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(pruneCmd())
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
