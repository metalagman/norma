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
	initcmd "github.com/normahq/norma/cmd/norma/init"
	loopcmd "github.com/normahq/norma/cmd/norma/loop"
	mcpcmd "github.com/normahq/norma/cmd/norma/mcp"
	plancmd "github.com/normahq/norma/cmd/norma/plan"
	playgroundcmd "github.com/normahq/norma/cmd/norma/playground"
	prunecmd "github.com/normahq/norma/cmd/norma/prune"
	runcmd "github.com/normahq/norma/cmd/norma/run"
	runscmd "github.com/normahq/norma/cmd/norma/runs"
	swarmcmd "github.com/normahq/norma/cmd/norma/swarm"
	toolcmd "github.com/normahq/norma/cmd/norma/tool"
	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configDir    string
	debug        bool
	trace        bool
	profile      string
	runBeadsInit = func(ctx context.Context, workingDir string) error {
		cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", "norma")
		cmd.Dir = workingDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	rootCmd = &cobra.Command{
		Use:   "codex",
		Short: "codex is a minimal agent workflow runner",
	}
)

// Execute runs the root command.
func Execute() error {
	cobra.OnInitialize(initDotEnv)
	rootCmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "extra config root directory (highest priority)")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&trace, "trace", false, "enable trace logging (overrides --debug)")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "config profile name")
	if err := viper.BindPFlag("config_dir", rootCmd.PersistentFlags().Lookup("config-dir")); err != nil {
		return fmt.Errorf("bind config-dir flag: %w", err)
	}
	if err := viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile")); err != nil {
		return fmt.Errorf("bind profile flag: %w", err)
	}
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		logLevel := logging.LevelInfo
		if debug {
			logLevel = logging.LevelDebug
		}
		if trace {
			logLevel = logging.LevelTrace
		}
		_ = logging.Init(logging.WithLevel(logLevel))
		workingDir, err := os.Getwd()
		if err != nil {
			log.Warn().Err(err).Msg("failed to get current working directory")
			return
		}
		if git.Available(cmd.Context(), workingDir) {
			if err := initBeads(cmd.Context()); err != nil {
				log.Warn().Err(err).Msg("failed to initialize beads")
			}
		}
	}
	rootCmd.AddCommand(loopcmd.Command())
	rootCmd.AddCommand(runcmd.Command())
	rootCmd.AddCommand(runscmd.Command())
	rootCmd.AddCommand(swarmcmd.Command())
	rootCmd.AddCommand(plancmd.Command())
	rootCmd.AddCommand(mcpcmd.Command())
	rootCmd.AddCommand(toolcmd.Command())
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
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %w", err)
	}

	topLevelOut, err := git.GitRunCmdOutput(ctx, workingDir, "git", "rev-parse", "--show-toplevel")
	if err == nil {
		workingDir = strings.TrimSpace(topLevelOut)
	}

	beadsPath := filepath.Join(workingDir, ".beads")
	if _, err := os.Stat(beadsPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat beads dir %q: %w", beadsPath, err)
	}

	log.Info().Str("path", beadsPath).Msg(".beads not found, initializing with prefix 'norma'")
	return runBeadsInit(ctx, workingDir)
}
