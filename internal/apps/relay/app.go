package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ipfans/fxlogger"
	relayagent "github.com/normahq/norma/internal/apps/relay/agent"
	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/handlers"
	relaystate "github.com/normahq/norma/internal/apps/relay/state"
	"github.com/normahq/norma/internal/apps/relay/tgbotkit"
	"github.com/normahq/norma/internal/apps/sessionmcp"
	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/agentfactory"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/rs/zerolog/log"
	"github.com/tgbotkit/runtime"
	"github.com/tgbotkit/runtime/updatepoller"
	"go.uber.org/fx"
)

// App creates a new fx.App for the relay bot with the provided configuration.
func App(cfg Config, normaCfg runtimeconfig.NormaConfig, ownerToken string) *fx.App {
	return fx.New(
		fx.WithLogger(
			fxlogger.WithZerolog(
				log.Logger.With().Str("component", "relay").Logger(),
			),
		),
		Module(cfg, normaCfg, ownerToken),
	)
}

// Module returns the fx.Module for the relay bot, initialized with the provided configurations.
func Module(cfg Config, normaCfg runtimeconfig.NormaConfig, ownerToken string) fx.Option {
	// Convert relay config to tgbotkit config.
	tgbotkitCfg := tgbotkit.Config{
		Token: cfg.Relay.Telegram.Token,
		Webhook: tgbotkit.WebhookConfig{
			Enabled:     cfg.Relay.Telegram.Webhook.Enabled,
			ListenAddr:  cfg.Relay.Telegram.Webhook.ListenAddr,
			Path:        cfg.Relay.Telegram.Webhook.Path,
			URL:         cfg.Relay.Telegram.Webhook.URL,
			SecretToken: cfg.Relay.Telegram.Webhook.SecretToken,
		},
	}

	logger := log.Logger.With().Str("component", "relay").Logger()

	workingDir, err := resolveWorkingDir(cfg.Relay.WorkingDir)
	if err != nil {
		return fx.Module("relay", fx.Error(fmt.Errorf("resolve relay working_dir: %w", err)))
	}
	stateDir, err := resolveStateDir(workingDir, cfg.Relay.StateDir)
	if err != nil {
		return fx.Module("relay", fx.Error(err))
	}

	// Start with global MCP servers.
	mcpServers := make(map[string]agentconfig.MCPServerConfig, len(normaCfg.MCPServers))
	for k, v := range normaCfg.MCPServers {
		mcpServers[k] = v
	}
	mcpReg := mcpregistry.New(mcpServers)
	// If relay MCP address is configured, pre-register the external relay endpoint.
	if cfg.Relay.MCP.Address != "" {
		mcpReg.Set("norma.relay", agentconfig.MCPServerConfig{
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  fmt.Sprintf("http://%s/mcp", cfg.Relay.MCP.Address),
		})
	}

	return fx.Module("relay",
		fx.Supply(
			tgbotkitCfg,
			logger,
			normaCfg,
			workingDir,
			mcpReg,
		),
		fx.Provide(
			func(lc fx.Lifecycle) (relaystate.Provider, error) {
				if err := os.MkdirAll(stateDir, 0o755); err != nil {
					return nil, fmt.Errorf("create relay state dir: %w", err)
				}
				dbPath := filepath.Join(stateDir, "relay.db")
				provider, err := relaystate.NewSQLiteProvider(context.Background(), dbPath)
				if err != nil {
					return nil, fmt.Errorf("open relay state provider: %w", err)
				}
				lc.Append(fx.Hook{
					OnStop: func(ctx context.Context) error {
						return provider.Close()
					},
				})
				return provider, nil
			},
			func(provider relaystate.Provider) updatepoller.OffsetStore {
				return provider.PollingOffsetStore()
			},
			func(provider relaystate.Provider) sessionmcp.Store {
				return provider.SessionMCPKV()
			},
		),
		fx.Provide(
			fx.Annotate(
				func() (bool, error) {
					mode, enabled, err := ResolveWorkspaceEnabled(
						context.Background(),
						cfg.Relay.Workspace.Mode,
						workingDir,
						git.Available,
					)
					if err != nil {
						return false, err
					}
					logger.Info().
						Str("workspace_mode", string(mode)).
						Bool("workspace_enabled", enabled).
						Str("working_dir", workingDir).
						Str("state_dir", stateDir).
						Msg("relay workspace mode resolved")
					return enabled, nil
				},
				fx.ResultTags(`name:"relay_workspace_enabled"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() string { return cfg.Relay.MCP.Address },
				fx.ResultTags(`name:"relay_mcp_addr"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() []string { return append([]string(nil), cfg.Relay.InternalMCP.Servers...) },
				fx.ResultTags(`name:"relay_internal_mcp_servers"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() string { return strings.TrimSpace(ownerToken) },
				fx.ResultTags(`name:"relay_auth_token"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() string {
					return cfg.Relay.RootAgent
				},
				fx.ResultTags(`name:"relay_root_agent"`),
			),
		),
		fx.Provide(func(provider relaystate.Provider) (*auth.OwnerStore, error) {
			return auth.NewOwnerStore(provider.AppKV())
		}),
		fx.Provide(func(reg *mcpregistry.MapRegistry) *agentfactory.Factory {
			return agentfactory.New(
				normaCfg.Agents,
				reg,
				agentfactory.WithPermissionHandler(relayagent.DefaultPermissionHandler),
			)
		}),
		tgbotkit.Module,
		handlers.Module,
		fx.Provide(
			handlers.NewInternalMCPManager,
		),
		// Start Telegram runtime only after internal MCP is started.
		fx.Invoke(func(lc fx.Lifecycle, bot *runtime.Bot, _ *handlers.InternalMCPManager) {
			runCtx, cancel := context.WithCancel(context.Background())
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					go func() {
						if err := bot.Run(runCtx); err != nil {
							if isExpectedBotRunShutdown(err) {
								bot.Logger().Debugf("bot run stopped during shutdown: %v", err)
								return
							}
							bot.Logger().Errorf("bot run failed: %v", err)
						}
					}()
					return nil
				},
				OnStop: func(ctx context.Context) error {
					cancel()
					return nil
				},
			})
		}),
	)
}

func resolveWorkingDir(raw string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current working directory: %w", err)
	}

	workingDir := strings.TrimSpace(raw)
	if workingDir == "" {
		return filepath.Clean(cwd), nil
	}
	if !filepath.IsAbs(workingDir) {
		workingDir = filepath.Join(cwd, workingDir)
	}

	resolved, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("resolve absolute working_dir %q: %w", raw, err)
	}
	return filepath.Clean(resolved), nil
}

func isExpectedBotRunShutdown(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled)
}

func resolveStateDir(workingDir, raw string) (string, error) {
	stateDir := strings.TrimSpace(raw)
	if stateDir == "" {
		return "", fmt.Errorf("relay.state_dir is required")
	}
	if !filepath.IsAbs(stateDir) {
		stateDir = filepath.Join(workingDir, stateDir)
	}

	resolved, err := filepath.Abs(stateDir)
	if err != nil {
		return "", fmt.Errorf("resolve absolute state_dir %q: %w", raw, err)
	}
	return filepath.Clean(resolved), nil
}
