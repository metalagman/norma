// Package main provides the entry point for the norma CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	initcmd "github.com/metalagman/norma/cmd/norma/init"
	loopcmd "github.com/metalagman/norma/cmd/norma/loop"
	plancmd "github.com/metalagman/norma/cmd/norma/plan"
	playgroundcmd "github.com/metalagman/norma/cmd/norma/playground"
	prunecmd "github.com/metalagman/norma/cmd/norma/prune"
	runcmd "github.com/metalagman/norma/cmd/norma/run"
	runscmd "github.com/metalagman/norma/cmd/norma/runs"
	taskcmd "github.com/metalagman/norma/cmd/norma/task"
	"github.com/metalagman/norma/internal/git"
	"github.com/metalagman/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile      string
	debug        bool
	profile      string
	runBeadsInit = func(ctx context.Context, repoRoot string) error {
		cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", "norma")
		cmd.Dir = repoRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	rootCmd = &cobra.Command{
		Use:   "norma",
		Short: "norma is a minimal agent workflow runner",
	}
)

// Execute runs the root command.
func Execute() error {
	cobra.OnInitialize(initDotEnv, initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", defaultConfigPath, "config file path")
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
			if err := initBeads(cmd.Context()); err != nil {
				log.Warn().Err(err).Msg("failed to initialize beads")
			}
		}
	}
	rootCmd.AddCommand(loopcmd.Command())
	rootCmd.AddCommand(runcmd.Command())
	rootCmd.AddCommand(runscmd.Command())
	rootCmd.AddCommand(taskcmd.Command())
	rootCmd.AddCommand(plancmd.Command())
	rootCmd.AddCommand(playgroundcmd.Command())
	rootCmd.AddCommand(initcmd.Command())
	rootCmd.AddCommand(prunecmd.Command())
	return rootCmd.Execute()
}

func initDotEnv() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cobra.CheckErr(fmt.Errorf(".env load: %w", err))
	}
}

func initBeads(ctx context.Context) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %w", err)
	}

	topLevelOut, err := git.GitRunCmdOutput(ctx, repoRoot, "git", "rev-parse", "--show-toplevel")
	if err == nil {
		repoRoot = strings.TrimSpace(topLevelOut)
	}

	beadsPath := filepath.Join(repoRoot, ".beads")
	if _, err := os.Stat(beadsPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat beads dir %q: %w", beadsPath, err)
	}

	log.Info().Str("path", beadsPath).Msg(".beads not found, initializing with prefix 'norma'")
	return runBeadsInit(ctx, repoRoot)
}

func initConfig() {
	path := cfgFile
	if path == "" {
		path = defaultConfigPath
	}
	viper.SetEnvPrefix("NORMA")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()
	viper.SetConfigFile(path)
}
