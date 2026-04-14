package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ipfans/fxlogger"
	relayagent "github.com/normahq/norma/internal/apps/relay/agent"
	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/handlers"
	"github.com/normahq/norma/internal/apps/relay/runtimecfg"
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

type workspaceBaseBranchParams struct {
	fx.In

	WorkspaceEnabled bool `name:"relay_workspace_enabled"`
}

const bundledRelayMCPServerID = "relay"

// App creates a new fx.App for the relay bot with the provided configuration.
func App(
	cfg Config,
	normaCfg runtimeconfig.NormaConfig,
	ownerToken string,
	runtimeLoadOpts runtimeconfig.RuntimeLoadOptions,
	defaultsYAML []byte,
) *fx.App {
	return fx.New(
		fx.WithLogger(
			fxlogger.WithZerolog(
				log.Logger.With().Str("component", "relay").Logger(),
			),
		),
		Module(cfg, normaCfg, ownerToken, runtimeLoadOpts, defaultsYAML),
	)
}

// Module returns the fx.Module for the relay bot, initialized with the provided configurations.
func Module(
	cfg Config,
	normaCfg runtimeconfig.NormaConfig,
	ownerToken string,
	runtimeLoadOpts runtimeconfig.RuntimeLoadOptions,
	defaultsYAML []byte,
) fx.Option {
	// Convert relay config to tgbotkit config.
	tgbotkitCfg := tgbotkit.Config{
		Token: cfg.Relay.Telegram.Token,
		Webhook: tgbotkit.WebhookConfig{
			Enabled:    cfg.Relay.Telegram.Webhook.Enabled,
			ListenAddr: cfg.Relay.Telegram.Webhook.ListenAddr,
			Path:       cfg.Relay.Telegram.Webhook.Path,
			URL:        cfg.Relay.Telegram.Webhook.URL,
			AuthToken:  cfg.Relay.Telegram.Webhook.AuthToken,
		},
	}

	logger := log.Logger.With().Str("component", "relay").Logger()
	workingDir, err := resolveWorkingDir(cfg.Relay.WorkingDir)
	if err != nil {
		return fx.Module("relay", fx.Error(fmt.Errorf("resolve relay working_dir: %w", err)))
	}
	configPath := relayConfigPath(workingDir)
	if err := validateRelayMCPConfiguration(cfg, normaCfg, configPath); err != nil {
		return fx.Module("relay", fx.Error(err))
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

	return fx.Module("relay",
		fx.Supply(
			tgbotkitCfg,
			logger,
			normaCfg,
			workingDir,
			mcpReg,
		),
		fx.Provide(
			func() *runtimecfg.Loader {
				return runtimecfg.NewLoader(runtimeLoadOpts, defaultsYAML)
			},
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
				func(p workspaceBaseBranchParams) (string, error) {
					baseBranch, source, err := resolveWorkspaceBaseBranch(
						context.Background(),
						workingDir,
						cfg.Relay.Workspace.BaseBranch,
						p.WorkspaceEnabled,
					)
					if err != nil {
						return "", err
					}
					logger.Info().
						Str("workspace_base_branch", baseBranch).
						Str("workspace_base_branch_source", source).
						Bool("workspace_enabled", p.WorkspaceEnabled).
						Msg("relay workspace base branch resolved")
					return baseBranch, nil
				},
				fx.ResultTags(`name:"relay_workspace_base_branch"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() []string { return append([]string(nil), cfg.Relay.MCPServers...) },
				fx.ResultTags(`name:"relay_mcp_servers"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() []string { return sortedMCPServerIDs(normaCfg.MCPServers) },
				fx.ResultTags(`name:"relay_runtime_mcp_server_ids"`),
			),
		),
		fx.Provide(
			fx.Annotate(
				func() string {
					return strings.TrimSpace(cfg.Relay.SystemInstructions)
				},
				fx.ResultTags(`name:"relay_system_instructions"`),
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
				normaCfg.Providers,
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

var removedBuiltInRelayMCPServerIDs = map[string]string{
	"norma.state":     bundledRelayMCPServerID,
	"norma.workspace": bundledRelayMCPServerID,
	"norma.relay":     bundledRelayMCPServerID,
	"relay.state":     bundledRelayMCPServerID,
	"relay.workspace": bundledRelayMCPServerID,
	"relay.agents":    bundledRelayMCPServerID,
}

var removedConfigMCPServerIDs = map[string]struct{}{
	"norma.config": {},
	"relay.config": {},
}

func validateRelayMCPConfiguration(cfg Config, normaCfg runtimeconfig.NormaConfig, configPath string) error {
	errs := make([]string, 0)

	for id := range normaCfg.MCPServers {
		switch id {
		case bundledRelayMCPServerID:
			errs = append(errs, `norma.mcp_servers.relay is reserved for the built-in relay MCP server`)
		default:
			if _, ok := removedConfigMCPServerIDs[id]; ok {
				errs = append(errs, fmt.Sprintf("norma.mcp_servers.%s conflicts with removed built-in config MCP server ID %q; edit the relay config file directly at %q", id, id, configPath))
			} else if replacement, ok := removedBuiltInRelayMCPServerIDs[id]; ok {
				errs = append(errs, fmt.Sprintf("norma.mcp_servers.%s conflicts with removed built-in MCP server ID %q; rename the custom server and use %q for the built-in relay MCP server", id, id, replacement))
			}
		}
	}

	for i, id := range cfg.Relay.MCPServers {
		trimmed := strings.TrimSpace(id)
		if _, ok := removedConfigMCPServerIDs[trimmed]; ok {
			errs = append(errs, fmt.Sprintf("relay.mcp_servers[%d] references removed built-in config MCP server %q; edit the relay config file directly at %q", i, id, configPath))
		} else if replacement, ok := removedBuiltInRelayMCPServerIDs[trimmed]; ok {
			errs = append(errs, fmt.Sprintf("relay.mcp_servers[%d] references removed built-in MCP server %q; use %q", i, id, replacement))
		}
	}

	for agentName, agentCfg := range normaCfg.Providers {
		for i, id := range agentCfg.MCPServers {
			trimmed := strings.TrimSpace(id)
			if _, ok := removedConfigMCPServerIDs[trimmed]; ok {
				errs = append(errs, fmt.Errorf("norma.providers.%s.mcp_servers[%d] references removed built-in config MCP server %q; edit the relay config file directly at %q", agentName, i, id, configPath).Error())
			} else if replacement, ok := removedBuiltInRelayMCPServerIDs[trimmed]; ok {
				errs = append(errs, fmt.Errorf("norma.providers.%s.mcp_servers[%d] references removed built-in MCP server %q; use %q", agentName, i, id, replacement).Error())
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("invalid relay MCP configuration: %s", strings.Join(errs, "; "))
}

func relayConfigPath(workingDir string) string {
	trimmed := strings.TrimSpace(workingDir)
	if trimmed == "" {
		return ".config/relay/config.yaml"
	}
	return filepath.Join(trimmed, ".config", "relay", "config.yaml")
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

func resolveWorkspaceBaseBranch(
	ctx context.Context,
	workingDir string,
	configuredBranch string,
	workspaceEnabled bool,
) (branch string, source string, err error) {
	configured := strings.TrimSpace(configuredBranch)
	if !workspaceEnabled {
		if configured == "" {
			return "", "disabled", nil
		}
		return configured, "config", nil
	}

	if configured != "" {
		ref := "refs/heads/" + configured
		if err := git.GitRunCmdErr(ctx, workingDir, "git", "show-ref", "--verify", "--quiet", ref); err == nil {
			return configured, "config", nil
		}
	}

	headBranch, err := git.CurrentBranch(ctx, workingDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve relay.workspace.base_branch: %w", err)
	}
	return headBranch, "head", nil
}

func sortedMCPServerIDs(servers map[string]agentconfig.MCPServerConfig) []string {
	if len(servers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(servers))
	for id := range servers {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		ids = append(ids, trimmed)
	}
	sort.Strings(ids)
	return ids
}
