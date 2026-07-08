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
	runtimedebug "runtime/debug"
	"strings"

	"github.com/joho/godotenv"
	goalkeepercmd "github.com/normahq/norma/v2/cmd/norma/goalkeeper"
	goalkeeperactorcmd "github.com/normahq/norma/v2/cmd/norma/goalkeeperactor"
	initcmd "github.com/normahq/norma/v2/cmd/norma/init"
	loopcmd "github.com/normahq/norma/v2/cmd/norma/loop"
	mcpcmd "github.com/normahq/norma/v2/cmd/norma/mcp"
	pdcasynccmd "github.com/normahq/norma/v2/cmd/norma/pdcasync"
	pdcataskmastercmd "github.com/normahq/norma/v2/cmd/norma/pdcataskmaster"
	plancmd "github.com/normahq/norma/v2/cmd/norma/plan"
	prunecmd "github.com/normahq/norma/v2/cmd/norma/prune"
	runcmd "github.com/normahq/norma/v2/cmd/norma/run"
	runscmd "github.com/normahq/norma/v2/cmd/norma/runs"
	swarmcmd "github.com/normahq/norma/v2/cmd/norma/swarm"
	taskmastercmd "github.com/normahq/norma/v2/cmd/norma/taskmaster"
	taskmasterchatcmd "github.com/normahq/norma/v2/cmd/norma/taskmasterchat"
	"github.com/normahq/norma/v2/internal/git"
	"github.com/normahq/norma/v2/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configDir    string
	debug        bool
	trace        bool
	profile      string
	version      = "dev"
	runBeadsInit = func(ctx context.Context, workingDir string) error {
		cmd := exec.CommandContext(ctx, "bd", "init", "--prefix", "norma")
		cmd.Dir = workingDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	rootCmd = &cobra.Command{
		Use:     "norma",
		Short:   "norma is an autonomous agent workflow orchestrator",
		Version: versionString(),
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
	rootCmd.AddCommand(goalkeepercmd.Command())
	rootCmd.AddCommand(goalkeeperactorcmd.Command())
	rootCmd.AddCommand(pdcasynccmd.Command())
	rootCmd.AddCommand(pdcataskmastercmd.Command())
	rootCmd.AddCommand(taskmastercmd.Command())
	rootCmd.AddCommand(taskmasterchatcmd.Command())
	rootCmd.AddCommand(initcmd.Command())
	rootCmd.AddCommand(prunecmd.Command())
	return rootCmd.Execute()
}

func initDotEnv() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cobra.CheckErr(fmt.Errorf(".env load: %w", err))
	}
}

func versionString() string {
	if strings.TrimSpace(version) != "" && version != "dev" {
		return version
	}
	info, ok := runtimedebug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
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
