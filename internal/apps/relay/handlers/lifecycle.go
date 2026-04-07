package handlers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/internal/apps/configmcp"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/normahq/norma/internal/apps/relay/session"
	"github.com/normahq/norma/internal/apps/relaymcp"
	"github.com/normahq/norma/internal/apps/sessionmcp"
	"github.com/normahq/norma/internal/apps/workspacemcp"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/rs/zerolog"
	"go.uber.org/fx"
)

// InternalMCPManager controls startup/shutdown of internal MCP servers configured for relay.
type InternalMCPManager struct {
	serverIDs        []string
	workspaceEnabled bool
	started          bool
	mu               sync.RWMutex
	logger           zerolog.Logger
	registry         mcpregistry.Registry
	workingDir       string
	sessionManager   *session.Manager
	messenger        *messenger.Messenger
	stateStore       sessionmcp.Store
	cleanups         []func() error
}

const (
	bundledConfigServerID    = "norma.config"
	bundledStateServerID     = "norma.state"
	bundledRelayServerID     = "norma.relay"
	bundledWorkspaceServerID = "norma.workspace"
)

type internalMCPParams struct {
	fx.In

	LC               fx.Lifecycle
	ServerIDs        []string `name:"relay_internal_mcp_servers"`
	WorkspaceEnabled bool     `name:"relay_workspace_enabled"`
	Logger           zerolog.Logger
	Registry         *mcpregistry.MapRegistry
	WorkingDir       string
	SessionManager   *session.Manager
	Messenger        *messenger.Messenger
	StateStore       sessionmcp.Store
	RelayMCPAddr     string `name:"relay_mcp_addr" optional:"true"`
}

// NewInternalMCPManager creates an internal MCP lifecycle manager.
func NewInternalMCPManager(params internalMCPParams) *InternalMCPManager {
	manager := &InternalMCPManager{
		serverIDs:        append([]string(nil), params.ServerIDs...),
		workspaceEnabled: params.WorkspaceEnabled,
		logger:           params.Logger.With().Str("component", "relay.internal_mcp").Logger(),
		registry:         params.Registry,
		workingDir:       params.WorkingDir,
		sessionManager:   params.SessionManager,
		messenger:        params.Messenger,
		stateStore:       params.StateStore,
	}

	params.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			manager.logger.Info().Int("servers", len(manager.serverIDs)).Msg("starting internal MCP servers")

			// 1. Ensure bundled core servers are started if not already configured.
			if err := manager.ensureBundledServers(ctx, params.RelayMCPAddr); err != nil {
				return fmt.Errorf("ensuring bundled servers: %w", err)
			}

			// 2. Log any other configured internal servers.
			for _, id := range manager.serverIDs {
				serverID := strings.TrimSpace(id)
				if serverID == "" || isBundled(serverID) {
					continue
				}
				manager.logger.Info().Str("server_id", serverID).Msg("internal MCP server configured")
			}

			manager.mu.Lock()
			manager.started = true
			manager.mu.Unlock()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			manager.mu.Lock()
			defer manager.mu.Unlock()

			manager.logger.Info().Int("cleanups", len(manager.cleanups)).Msg("stopping internal MCP servers")
			for i := len(manager.cleanups) - 1; i >= 0; i-- {
				if err := manager.cleanups[i](); err != nil {
					manager.logger.Warn().Err(err).Msg("failed to stop internal MCP server")
				}
			}
			manager.cleanups = nil
			manager.started = false
			return nil
		},
	})

	return manager
}

func (m *InternalMCPManager) ensureBundledServers(ctx context.Context, relayMCPAddr string) error {
	if m.stateStore == nil {
		return fmt.Errorf("relay state store is required")
	}

	handlersByID := make(map[string]http.Handler, 4)
	routes := make([]string, 0, 5)

	// norma.config
	if _, ok := m.registry.Get(bundledConfigServerID); !ok {
		configPath := selectConfigPath(m.workingDir, "relay")
		svc, err := configmcp.NewConfigService(configPath)
		if err != nil {
			m.logger.Warn().Err(err).Msg("failed to create config service")
		} else {
			server, err := configmcp.NewServer(svc)
			if err != nil {
				return fmt.Errorf("build bundled config MCP server: %w", err)
			}
			handlersByID[bundledConfigServerID] = streamableHandlerForServer(server)
			routes = append(routes, bundledRoutePath(bundledConfigServerID))
		}
	}

	// norma.state
	if _, ok := m.registry.Get(bundledStateServerID); !ok {
		server, err := sessionmcp.NewServer(m.stateStore)
		if err != nil {
			return fmt.Errorf("build bundled state MCP server: %w", err)
		}
		handlersByID[bundledStateServerID] = streamableHandlerForServer(server)
		routes = append(routes, bundledRoutePath(bundledStateServerID))
	}

	// norma.relay
	if _, ok := m.registry.Get(bundledRelayServerID); !ok || relayMCPAddr != "" {
		// If it's already in factory but relayMCPAddr is set, it means it was configured in app.go
		// and we should start it on that address.
		// If it's NOT in factory, we start it on a random port.
		svc := session.NewRelayMCPServer(m.sessionManager, m.messenger)
		server, err := relaymcp.NewServer(svc)
		if err != nil {
			return fmt.Errorf("build bundled relay MCP server: %w", err)
		}
		handlersByID[bundledRelayServerID] = streamableHandlerForServer(server)
		routes = append(routes, bundledRoutePath(bundledRelayServerID), "/mcp")
	}

	// norma.workspace
	if m.workspaceEnabled {
		if _, ok := m.registry.Get(bundledWorkspaceServerID); !ok {
			svc := session.NewWorkspaceMCPServer(m.sessionManager)
			server, err := workspacemcp.NewServer(svc)
			if err != nil {
				return fmt.Errorf("build bundled workspace MCP server: %w", err)
			}
			handlersByID[bundledWorkspaceServerID] = streamableHandlerForServer(server)
			routes = append(routes, bundledRoutePath(bundledWorkspaceServerID))
		}
	} else {
		m.logger.Info().Msg("workspace mode disabled; skipping bundled workspace server")
	}

	if len(handlersByID) == 0 {
		return nil
	}

	listenAddr := strings.TrimSpace(relayMCPAddr)
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}

	res, err := startBundledMCPHTTPServer(ctx, listenAddr, handlersByID)
	if err != nil {
		return fmt.Errorf("start bundled MCP listener: %w", err)
	}
	m.addCleanup(res.Close)

	ids := make([]string, 0, len(handlersByID))
	for id := range handlersByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m.registry.Set(id, agentconfig.MCPServerConfig{
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  bundledRegistryURL(res.Addr, id),
		})
	}

	sort.Strings(routes)
	m.logger.Info().
		Str("addr", res.Addr).
		Str("routes", strings.Join(routes, ", ")).
		Msg("bundled MCP listener started")

	return nil
}

func streamableHandlerForServer(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{})
}

func bundledRoutePath(serverID string) string {
	return "/mcp/" + serverID
}

func bundledRegistryURL(addr, serverID string) string {
	if serverID == bundledRelayServerID {
		return fmt.Sprintf("http://%s/mcp", addr)
	}
	return fmt.Sprintf("http://%s%s", addr, bundledRoutePath(serverID))
}

type bundledHTTPServerResult struct {
	Addr  string
	Close func() error
}

func startBundledMCPHTTPServer(ctx context.Context, addr string, handlersByID map[string]http.Handler) (*bundledHTTPServerResult, error) {
	mux := http.NewServeMux()

	ids := make([]string, 0, len(handlersByID))
	for id := range handlersByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		handler := handlersByID[id]
		mux.Handle(bundledRoutePath(id), handler)
		if id == bundledRelayServerID {
			mux.Handle("/mcp", handler)
		}
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}

	httpServer := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	go func() {
		_ = httpServer.Serve(listener)
	}()

	return &bundledHTTPServerResult{
		Addr: listener.Addr().String(),
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

func selectConfigPath(workDir, appName string) string {
	if appName == "relay" {
		return filepath.Join(workDir, ".config", "relay", "config.yaml")
	}

	appPath := filepath.Join(workDir, ".norma", appName+".yaml")
	if info, err := os.Stat(appPath); err == nil && !info.IsDir() {
		return appPath
	}
	return filepath.Join(workDir, ".norma", "config.yaml")
}

func (m *InternalMCPManager) addCleanup(f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups = append(m.cleanups, f)
}

func isBundled(id string) bool {
	switch id {
	case bundledConfigServerID, bundledStateServerID, bundledRelayServerID, bundledWorkspaceServerID:
		return true
	default:
		return false
	}
}

// Started reports whether internal MCP startup hook has completed.
func (m *InternalMCPManager) Started() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}
