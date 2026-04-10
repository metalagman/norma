package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/normahq/norma/internal/apps/relay"
	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//go:embed relay.yaml
var defaultRelayConfig []byte

const shutdownTimeout = 10 * time.Second

type relayConfigDocument struct {
	Norma appconfig.NormaConfig `mapstructure:"norma"`
	Relay relay.RelayConfig     `mapstructure:"relay"`
}

func startCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start Telegram relay bot",
		Long:  "Start the Telegram relay bot server. A random owner token will be generated and displayed.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}

			var doc relayConfigDocument
			_, err = appconfig.LoadConfigDocument(
				appconfig.RuntimeLoadOptions{
					WorkingDir: workingDir,
					ConfigDir:  viper.GetString("config_dir"),
					Profile:    viper.GetString("profile"),
				},
				appconfig.AppLoadOptions{
					AppName:      "relay",
					DefaultsYAML: defaultRelayConfig,
				},
				&doc,
			)
			if err != nil {
				return err
			}
			if err := applyRelayLogging(doc.Relay.Logger); err != nil {
				return fmt.Errorf("configure relay logging: %w", err)
			}

			relayCfg := relay.Config{Relay: doc.Relay}

			if relayCfg.Relay.Telegram.Token == "" {
				return fmt.Errorf("telegram token is required\nSet it via:\n  - Environment: RELAY_TELEGRAM_TOKEN=<token>\n  - App config: relay.telegram.token in .config/relay/config.yaml\n  - Profile override: profiles.<name>.relay.telegram.token in the same file")
			}

			ownerToken, err := auth.GenerateOwnerToken()
			if err != nil {
				return fmt.Errorf("generating owner token: %w", err)
			}

			runtimeLoadOpts := appconfig.RuntimeLoadOptions{
				WorkingDir: workingDir,
				ConfigDir:  viper.GetString("config_dir"),
				Profile:    viper.GetString("profile"),
			}

			app := relay.App(relayCfg, doc.Norma, ownerToken, runtimeLoadOpts, defaultRelayConfig)

			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if err := app.Start(ctx); err != nil {
				return fmt.Errorf("starting relay app: %w", err)
			}

			logRelayStartup(ctx, relayCfg.Relay.Telegram.Token, ownerToken)

			<-ctx.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer shutdownCancel()
			if err := app.Stop(shutdownCtx); err != nil {
				return fmt.Errorf("stopping relay app: %w", err)
			}

			return nil
		},
	}

	return cmd
}

func serveCommand() *cobra.Command {
	cmd := startCommand()
	cmd.Use = "serve"
	cmd.Aliases = []string{"start"}
	return cmd
}
