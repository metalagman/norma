package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	relayagent "github.com/normahq/norma/internal/apps/relay/agent"
	"github.com/normahq/norma/internal/apps/relay/runtimecfg"
	relaystate "github.com/normahq/norma/internal/apps/relay/state"
	"github.com/normahq/norma/internal/apps/relaymcp"
	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/agentfactory"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/rs/zerolog"
	"go.uber.org/fx"
)

const cleanupTimeout = 10 * time.Second

const sessionStatusPersisted = "persisted"

var ErrNoPersistedSession = errors.New("no persisted session")

// Manager manages relay ADK sessions and persists session metadata.
type Manager struct {
	agentBuilder      *relayagent.Builder
	relayMCPServerIDs []string
	runtimeMCPIDs     map[string]struct{}
	rootAgentName     string
	workingDir        string
	workspaces        *relayagent.WorkspaceManager
	workspaceEnabled  bool
	workspaceBaseRef  string
	sessionStore      relaystate.SessionStore
	mcpRegistry       mcpregistry.Registry
	logger            zerolog.Logger

	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu       sync.RWMutex
	sessions map[string]*TopicSession
}

// ManagerParams provides dependencies for Manager.
type ManagerParams struct {
	fx.In

	LC                fx.Lifecycle
	AgentBuilder      *relayagent.Builder
	RelayMCPServerIDs []string `name:"relay_mcp_servers"`
	RuntimeMCPIDs     []string `name:"relay_runtime_mcp_server_ids"`
	RootAgentName     string   `name:"relay_root_agent"`
	WorkingDir        string
	WorkspaceEnabled  bool   `name:"relay_workspace_enabled"`
	WorkspaceBaseRef  string `name:"relay_workspace_base_branch"`
	StateProvider     relaystate.Provider
	MCPRegistry       *mcpregistry.MapRegistry
	Logger            zerolog.Logger
}

// NewManager creates a session Manager.
func NewManager(p ManagerParams) (*Manager, error) {
	if p.StateProvider == nil {
		return nil, fmt.Errorf("relay state provider is required")
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())

	m := &Manager{
		agentBuilder:      p.AgentBuilder,
		relayMCPServerIDs: append([]string(nil), p.RelayMCPServerIDs...),
		runtimeMCPIDs:     stringSet(p.RuntimeMCPIDs),
		rootAgentName:     strings.TrimSpace(p.RootAgentName),
		workingDir:        p.WorkingDir,
		workspaces:        relayagent.NewWorkspaceManager(p.WorkingDir, p.WorkspaceBaseRef),
		workspaceEnabled:  p.WorkspaceEnabled,
		workspaceBaseRef:  p.WorkspaceBaseRef,
		sessionStore:      p.StateProvider.Sessions(),
		mcpRegistry:       p.MCPRegistry,
		logger:            p.Logger.With().Str("component", "relay.session_manager").Logger(),
		rootCtx:           rootCtx,
		rootCancel:        rootCancel,
		sessions:          make(map[string]*TopicSession),
	}

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			m.logger.Info().Msg("session manager started")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			m.logger.Info().Int("active_sessions", len(m.sessions)).Msg("session manager stopping")
			m.rootCancel()
			m.stopAllWithContext(ctx)
			return nil
		},
	})

	return m, nil
}

// ValidateAgent checks if an agent with the given name exists in the config.
func (m *Manager) ValidateAgent(agentName string) error {
	m.mu.RLock()
	builder := m.agentBuilder
	m.mu.RUnlock()
	if builder == nil {
		return fmt.Errorf("agent builder is required")
	}
	return builder.ValidateAgent(agentName)
}

// GetAgentInfo returns the description and list of MCP server names for an agent.
func (m *Manager) GetAgentInfo(agentName string) (string, []string) {
	m.mu.RLock()
	builder := m.agentBuilder
	relayMCPServerIDs := append([]string(nil), m.relayMCPServerIDs...)
	m.mu.RUnlock()
	if builder == nil {
		return agentName, relayMCPServerIDs
	}
	description, mcpServers := builder.GetAgentInfo(agentName)
	return description, mergeUniqueStringIDs(mcpServers, relayMCPServerIDs)
}

// SessionBranchName returns the git branch name for a relay session.
func (m *Manager) SessionBranchName(sessionID string) string {
	return fmt.Sprintf("norma/relay/%s", sessionID)
}

func mergeUniqueStringIDs(base, extra []string) []string {
	if len(extra) == 0 {
		return append([]string(nil), base...)
	}

	out := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))
	appendUnique := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, id := range base {
		appendUnique(id)
	}
	for _, id := range extra {
		appendUnique(id)
	}

	return out
}

func (m *Manager) extraMCPServerIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.relayMCPServerIDs) == 0 {
		return nil
	}
	return append([]string(nil), m.relayMCPServerIDs...)
}

func relaySessionMCPServerID(sessionID string) string {
	return "relay.session." + strings.TrimSpace(sessionID)
}

func (m *Manager) sessionMCPServerIDs(sessionID string) ([]string, func(), error) {
	relayID, cleanup, err := m.ensureSessionRelayMCPServer(sessionID)
	if err != nil {
		return nil, nil, err
	}
	return mergeUniqueStringIDs([]string{relayID}, m.extraMCPServerIDs()), cleanup, nil
}

func (m *Manager) ensureSessionRelayMCPServer(sessionID string) (string, func(), error) {
	if m.mcpRegistry == nil {
		return "relay", func() {}, nil
	}

	baseCfg, ok := m.mcpRegistry.Get("relay")
	if !ok {
		return "", nil, fmt.Errorf("built-in relay MCP server is not registered")
	}

	headers := cloneStringMap(baseCfg.Headers)
	if headers == nil {
		headers = make(map[string]string, 1)
	}
	headers[relaymcp.CallerSessionIDHeader] = strings.TrimSpace(sessionID)

	cfg := agentconfig.MCPServerConfig{
		Type:       baseCfg.Type,
		Cmd:        append([]string(nil), baseCfg.Cmd...),
		Args:       append([]string(nil), baseCfg.Args...),
		Env:        cloneStringMap(baseCfg.Env),
		WorkingDir: baseCfg.WorkingDir,
		URL:        baseCfg.URL,
		Headers:    headers,
	}

	id := relaySessionMCPServerID(sessionID)
	m.mcpRegistry.Set(id, cfg)
	return id, func() {
		m.mcpRegistry.Delete(id)
	}, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ApplyRuntimeConfig updates manager runtime configuration used for creating new sessions.
func (m *Manager) ApplyRuntimeConfig(normaCfg runtimeconfig.RuntimeConfig, relayCfg runtimecfg.RelayConfig) error {
	builder, err := m.rebuildAgentBuilder(normaCfg, relayCfg)
	if err != nil {
		return err
	}

	runtimeMCPIDs := sortedStringSetKeys(normaCfg.MCPServers)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mcpRegistry != nil {
		for id := range m.runtimeMCPIDs {
			m.mcpRegistry.Delete(id)
		}
		for id, cfg := range normaCfg.MCPServers {
			m.mcpRegistry.Set(id, cfg)
		}
	}
	m.runtimeMCPIDs = stringSet(runtimeMCPIDs)
	m.relayMCPServerIDs = append([]string(nil), relayCfg.MCPServers...)
	m.rootAgentName = strings.TrimSpace(relayCfg.RootAgent)
	m.agentBuilder = builder
	return nil
}

func (m *Manager) rebuildAgentBuilder(normaCfg runtimeconfig.RuntimeConfig, relayCfg runtimecfg.RelayConfig) (*relayagent.Builder, error) {
	factory := agentfactory.New(
		normaCfg.Providers,
		m.mcpRegistry,
		agentfactory.WithPermissionHandler(relayagent.DefaultPermissionHandler),
	)

	return relayagent.NewBuilder(relayagent.BuilderParams{
		Factory:                factory,
		NormaCfg:               normaCfg,
		WorkingDir:             m.workingDir,
		WorkspaceEnabled:       m.workspaceEnabled,
		WorkspaceBaseBranch:    m.workspaceBaseRef,
		RelaySystemInstruction: relayCfg.SystemInstructions,
	}), nil
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out[trimmed] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedStringSetKeys(values map[string]agentconfig.MCPServerConfig) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

// CreateSession builds an agent for the given locator and stores it in memory.
func (m *Manager) CreateSession(ctx context.Context, sessionCtx SessionContext, agentName string) error {
	locator := sessionCtx.Locator
	userID := strings.TrimSpace(sessionCtx.UserID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}

	addr, ok, err := locator.TelegramAddress()
	if err != nil {
		return fmt.Errorf("decode session locator: %w", err)
	}
	if !ok {
		return fmt.Errorf("unsupported channel type %q", locator.ChannelType)
	}

	sessionID := strings.TrimSpace(locator.SessionID)
	chatID := addr.ChatID
	topicID := addr.TopicID
	m.mu.RLock()
	builder := m.agentBuilder
	m.mu.RUnlock()
	if builder == nil {
		return fmt.Errorf("agent builder is required")
	}

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("user_id", userID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Str("channel_type", locator.ChannelType).
		Msg("creating session")

	m.mu.Lock()
	if _, exists := m.sessions[sessionID]; exists {
		m.mu.Unlock()
		m.logger.Warn().Str("session_id", sessionID).Msg("session already exists")
		return fmt.Errorf("session already exists for %s", locator.AddressKey)
	}
	m.mu.Unlock()

	branchName := ""
	workspaceDir := m.workingDir
	if m.workspaceEnabled {
		branchName = m.SessionBranchName(sessionID)
		workspaceDir, err = m.workspaces.EnsureWorkspace(ctx, sessionID, branchName, "")
		if err != nil {
			m.logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to create workspace")
			return fmt.Errorf("create workspace: %w", err)
		}
		m.logger.Debug().Str("session_id", sessionID).Str("workspace", workspaceDir).Msg("workspace created")
	}

	mcpServerIDs, cleanupRelayMCP, err := m.sessionMCPServerIDs(sessionID)
	if err != nil {
		if m.workspaceEnabled {
			_ = m.workspaces.CleanupWorkspace(ctx, workspaceDir)
		}
		return fmt.Errorf("prepare session MCP servers: %w", err)
	}

	built, err := builder.BuildWithMCPServerIDs(
		m.rootCtx,
		sessionID,
		userID,
		chatID,
		topicID,
		agentName,
		workspaceDir,
		mcpServerIDs,
		nil,
	)
	if err != nil {
		m.logger.Error().Err(err).Str("session_id", sessionID).Str("agent", agentName).Msg("failed to build agent")
		cleanupRelayMCP()
		if m.workspaceEnabled {
			_ = m.workspaces.CleanupWorkspace(ctx, workspaceDir)
		}
		return err
	}

	ts := &TopicSession{
		sessionID:    sessionID,
		userID:       userID,
		locator:      locator,
		topicID:      topicID,
		agentName:    agentName,
		agent:        built.Agent,
		runner:       built.Runner,
		sessionSvc:   built.SessionSvc,
		sess:         built.Session,
		chatID:       chatID,
		workspaceDir: workspaceDir,
		branchName:   branchName,
		relayMCPID:   relaySessionMCPServerID(sessionID),
	}

	if err := m.persistSessionRecord(ctx, ts, relaystate.SessionStatusActive); err != nil {
		if closer, ok := ts.agent.(io.Closer); ok {
			_ = closer.Close()
		}
		cleanupRelayMCP()
		if m.workspaceEnabled && workspaceDir != "" {
			_ = m.workspaces.CleanupWorkspace(ctx, workspaceDir)
		}
		return fmt.Errorf("persist session metadata: %w", err)
	}

	m.mu.Lock()
	m.sessions[sessionID] = ts
	m.mu.Unlock()

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("user_id", userID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Str("channel_type", locator.ChannelType).
		Msg("session created successfully")

	return nil
}

// GetSession returns the in-memory session for the given locator.
func (m *Manager) GetSession(locator SessionLocator) (*TopicSession, error) {
	sessionID := strings.TrimSpace(locator.SessionID)

	m.mu.RLock()
	ts := m.sessions[sessionID]
	m.mu.RUnlock()

	if ts == nil {
		m.logger.Debug().
			Str("session_id", sessionID).
			Str("channel_type", locator.ChannelType).
			Str("address_key", locator.AddressKey).
			Int("active_sessions", len(m.sessions)).
			Msg("session not found")
		return nil, fmt.Errorf("no session for %s", locator.AddressKey)
	}

	return ts, nil
}

// GetTelegramSession returns the in-memory session for the given Telegram tuple.
func (m *Manager) GetTelegramSession(chatID int64, topicID int) (*TopicSession, error) {
	return m.GetSession(NewTelegramSessionLocator(chatID, topicID))
}

// EnsureSession returns the existing session or creates a new one if it doesn't exist.
func (m *Manager) EnsureSession(ctx context.Context, sessionCtx SessionContext, agentName string) (*TopicSession, error) {
	sessionID := strings.TrimSpace(sessionCtx.Locator.SessionID)

	m.mu.RLock()
	ts := m.sessions[sessionID]
	m.mu.RUnlock()

	if ts != nil {
		m.logger.Debug().Str("session_id", sessionID).Msg("returning existing session")
		return ts, nil
	}

	if err := m.CreateSession(ctx, sessionCtx, agentName); err != nil {
		return nil, err
	}
	return m.GetSession(sessionCtx.Locator)
}

// EnsureTelegramSession returns the existing Telegram session or creates a new one.
func (m *Manager) EnsureTelegramSession(ctx context.Context, chatID int64, topicID int, userID int64, agentName string) (*TopicSession, error) {
	return m.EnsureSession(ctx, SessionContext{
		Locator: NewTelegramSessionLocator(chatID, topicID),
		UserID:  TelegramUserID(userID),
	}, agentName)
}

// RestoreSession restores a session from persisted metadata when it is not active in memory.
func (m *Manager) RestoreSession(ctx context.Context, sessionCtx SessionContext) (*TopicSession, error) {
	locator := sessionCtx.Locator
	sessionID := strings.TrimSpace(locator.SessionID)

	m.mu.RLock()
	if ts := m.sessions[sessionID]; ts != nil {
		m.mu.RUnlock()
		return ts, nil
	}
	m.mu.RUnlock()

	record, ok, err := m.sessionStore.GetByAddress(ctx, locator.ChannelType, locator.AddressKey)
	if err != nil {
		return nil, fmt.Errorf("read session metadata: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w for %s", ErrNoPersistedSession, locator.AddressKey)
	}
	if strings.TrimSpace(record.Status) != "" && record.Status != relaystate.SessionStatusActive {
		return nil, fmt.Errorf("persisted session for %s is not active", locator.AddressKey)
	}

	recordLocator, err := LocatorFromRecord(record)
	if err != nil {
		return nil, fmt.Errorf("decode persisted session locator: %w", err)
	}

	resolvedAgentName, usedFallback, fallbackReason, err := resolveRestoreAgentName(
		record.AgentName,
		m.getRootAgentName(),
		m.ValidateAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve persisted session agent for %s: %w", locator.AddressKey, err)
	}

	m.logger.Info().
		Str("session_id", sessionID).
		Str("channel_type", recordLocator.ChannelType).
		Str("address_key", recordLocator.AddressKey).
		Str("agent", record.AgentName).
		Msg("restoring session from persisted metadata")
	if usedFallback {
		m.logger.Warn().
			Str("session_id", sessionID).
			Str("channel_type", recordLocator.ChannelType).
			Str("address_key", recordLocator.AddressKey).
			Str("persisted_agent", strings.TrimSpace(record.AgentName)).
			Str("fallback_agent", resolvedAgentName).
			Str("reason", fallbackReason).
			Msg("persisted session agent unavailable; falling back to root agent")
	}

	return m.EnsureSession(ctx, SessionContext{
		Locator: recordLocator,
		UserID:  sessionCtx.UserID,
	}, resolvedAgentName)
}

// RestoreTelegramSession restores a Telegram session from persisted metadata.
func (m *Manager) RestoreTelegramSession(ctx context.Context, chatID int64, topicID int, userID int64) (*TopicSession, error) {
	return m.RestoreSession(ctx, SessionContext{
		Locator: NewTelegramSessionLocator(chatID, topicID),
		UserID:  TelegramUserID(userID),
	})
}

// StopSession removes a session from memory and cleans up.
func (m *Manager) StopSession(locator SessionLocator) {
	sessionID := strings.TrimSpace(locator.SessionID)

	m.logger.Info().
		Str("session_id", sessionID).
		Str("channel_type", locator.ChannelType).
		Str("address_key", locator.AddressKey).
		Msg("stopping session")

	m.mu.Lock()
	ts, exists := m.sessions[sessionID]
	if exists {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !exists {
		m.logger.Warn().Str("session_id", sessionID).Msg("session not found for stop")
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := m.closeTopicSession(cleanupCtx, ts); err != nil {
		m.logger.Warn().Err(err).Str("session_id", sessionID).Msg("failed to close topic session")
	}
	if err := m.sessionStore.DeleteBySessionID(cleanupCtx, sessionID); err != nil {
		m.logger.Warn().Err(err).Str("session_id", sessionID).Msg("failed to delete persisted session metadata")
	}

	m.logger.Info().Str("session_id", sessionID).Msg("session stopped")
}

// StopTelegramSession removes a Telegram session from memory and cleans up.
func (m *Manager) StopTelegramSession(chatID int64, topicID int) {
	m.StopSession(NewTelegramSessionLocator(chatID, topicID))
}

// StopAll closes all sessions.
func (m *Manager) StopAll() {
	m.stopAllWithContext(context.Background())
}

func (m *Manager) stopAllWithContext(ctx context.Context) {
	m.mu.Lock()
	sessions := make([]*TopicSession, 0, len(m.sessions))
	for _, ts := range m.sessions {
		sessions = append(sessions, ts)
	}
	m.sessions = make(map[string]*TopicSession)
	m.mu.Unlock()

	m.logger.Info().Int("count", len(sessions)).Msg("stopping all sessions")

	for _, ts := range sessions {
		if err := m.closeTopicSession(ctx, ts); err != nil {
			m.logger.Warn().Err(err).Str("session_id", ts.sessionID).Msg("failed to close topic session")
		}
	}

	m.logger.Info().Msg("all sessions stopped")
}

// ListSessions returns info about all active sessions.
func (m *Manager) ListSessions() []TopicSessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]TopicSessionInfo, 0, len(m.sessions))
	for _, ts := range m.sessions {
		out = append(out, topicSessionInfo(ts, relaystate.SessionStatusActive))
	}
	return out
}

type TopicSessionInfo struct {
	SessionID    string
	UserID       string
	Locator      SessionLocator
	ChannelType  string
	AgentName    string
	ChatID       int64
	TopicID      int
	WorkspaceDir string
	BranchName   string
	Status       string
}

func (m *Manager) GetSessionInfo(ctx context.Context, sessionID string) (TopicSessionInfo, error) {
	trimmedID := strings.TrimSpace(sessionID)
	if trimmedID == "" {
		return TopicSessionInfo{}, fmt.Errorf("session_id is required")
	}

	m.mu.RLock()
	ts := m.sessions[trimmedID]
	m.mu.RUnlock()
	if ts != nil {
		return topicSessionInfo(ts, relaystate.SessionStatusActive), nil
	}

	record, ok, err := m.sessionStore.GetBySessionID(ctx, trimmedID)
	if err != nil {
		return TopicSessionInfo{}, fmt.Errorf("read session metadata: %w", err)
	}
	if !ok {
		return TopicSessionInfo{}, fmt.Errorf("session %q not found", trimmedID)
	}

	info, err := topicSessionInfoFromRecord(record)
	if err != nil {
		return TopicSessionInfo{}, err
	}
	info.Status = sessionStatusForInactiveRecord(record.Status)
	return info, nil
}

func (m *Manager) ListSessionInfos(ctx context.Context) ([]TopicSessionInfo, error) {
	persisted, err := m.sessionStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list session metadata: %w", err)
	}

	infos := make(map[string]TopicSessionInfo, len(persisted))
	for _, record := range persisted {
		info, err := topicSessionInfoFromRecord(record)
		if err != nil {
			return nil, err
		}
		info.Status = sessionStatusForInactiveRecord(record.Status)
		infos[info.SessionID] = info
	}

	for _, info := range m.ListSessions() {
		info.Status = relaystate.SessionStatusActive
		infos[info.SessionID] = info
	}

	out := make([]TopicSessionInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, info)
	}
	return out, nil
}

func (m *Manager) StopSessionByID(ctx context.Context, sessionID string) error {
	info, err := m.GetSessionInfo(ctx, sessionID)
	if err != nil {
		return err
	}
	if info.Status == relaystate.SessionStatusActive {
		m.StopSession(info.Locator)
		return nil
	}

	if m.workspaceEnabled && strings.TrimSpace(info.WorkspaceDir) != "" {
		if err := m.workspaces.CleanupWorkspace(ctx, info.WorkspaceDir); err != nil {
			return fmt.Errorf("cleanup workspace: %w", err)
		}
	}
	if err := m.sessionStore.DeleteBySessionID(ctx, info.SessionID); err != nil {
		return fmt.Errorf("delete session metadata: %w", err)
	}
	return nil
}

func topicSessionInfo(ts *TopicSession, status string) TopicSessionInfo {
	if ts == nil {
		return TopicSessionInfo{}
	}
	return TopicSessionInfo{
		SessionID:    ts.sessionID,
		UserID:       ts.userID,
		Locator:      ts.locator,
		ChannelType:  ts.locator.ChannelType,
		AgentName:    ts.agentName,
		ChatID:       ts.chatID,
		TopicID:      ts.topicID,
		WorkspaceDir: ts.workspaceDir,
		BranchName:   ts.branchName,
		Status:       status,
	}
}

func topicSessionInfoFromRecord(record relaystate.SessionRecord) (TopicSessionInfo, error) {
	locator, err := LocatorFromRecord(record)
	if err != nil {
		return TopicSessionInfo{}, fmt.Errorf("decode persisted session locator for %q: %w", record.SessionID, err)
	}

	info := TopicSessionInfo{
		SessionID:    record.SessionID,
		Locator:      locator,
		ChannelType:  locator.ChannelType,
		AgentName:    record.AgentName,
		WorkspaceDir: record.WorkspaceDir,
		BranchName:   record.BranchName,
	}
	if address, ok, err := locator.TelegramAddress(); err != nil {
		return TopicSessionInfo{}, fmt.Errorf("decode telegram address for %q: %w", record.SessionID, err)
	} else if ok {
		info.ChatID = address.ChatID
		info.TopicID = address.TopicID
	}
	return info, nil
}

func sessionStatusForInactiveRecord(recordStatus string) string {
	if strings.TrimSpace(recordStatus) == "" || recordStatus == relaystate.SessionStatusActive {
		return sessionStatusPersisted
	}
	return recordStatus
}

func resolveRestoreAgentName(
	persistedAgentName string,
	rootAgentName string,
	validate func(agentName string) error,
) (resolvedAgentName string, usedFallback bool, fallbackReason string, err error) {
	if validate == nil {
		return "", false, "", fmt.Errorf("agent validator is required")
	}

	persisted := strings.TrimSpace(persistedAgentName)
	root := strings.TrimSpace(rootAgentName)

	if persisted != "" {
		persistedErr := validate(persisted)
		if persistedErr == nil {
			return persisted, false, "", nil
		}

		if root == "" {
			return "", false, "", fmt.Errorf("persisted agent %q is unavailable: %w; relay root agent is not configured", persisted, persistedErr)
		}
		if rootErr := validate(root); rootErr != nil {
			return "", false, "", fmt.Errorf("persisted agent %q is unavailable: %w; relay root agent %q is unavailable: %w", persisted, persistedErr, root, rootErr)
		}
		return root, true, "persisted_agent_unavailable", nil
	}

	if root == "" {
		return "", false, "", fmt.Errorf("persisted session agent is empty and relay root agent is not configured")
	}
	if rootErr := validate(root); rootErr != nil {
		return "", false, "", fmt.Errorf("persisted session agent is empty and relay root agent %q is unavailable: %w", root, rootErr)
	}
	return root, true, "persisted_agent_missing", nil
}

func (m *Manager) getRootAgentName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rootAgentName
}

func (m *Manager) closeTopicSession(ctx context.Context, ts *TopicSession) error {
	var firstErr error
	if closer, ok := ts.agent.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			firstErr = err
		}
	}
	if m.workspaceEnabled && ts.workspaceDir != "" {
		if err := m.workspaces.CleanupWorkspace(ctx, ts.workspaceDir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if m.mcpRegistry != nil && strings.TrimSpace(ts.relayMCPID) != "" {
		m.mcpRegistry.Delete(ts.relayMCPID)
	}
	return firstErr
}

func (m *Manager) CommitWorkspace(ctx context.Context, chatID int64, topicID int) error {
	if !m.workspaceEnabled {
		return fmt.Errorf("workspace mode is disabled")
	}

	sessionID := NewTelegramSessionLocator(chatID, topicID).SessionID

	m.mu.RLock()
	ts, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no session for topic %d", topicID)
	}

	workspaceDir := ts.workspaceDir
	if workspaceDir == "" {
		return fmt.Errorf("no workspace for topic %d", topicID)
	}

	statusOut, err := git.GitRunCmdOutput(ctx, workspaceDir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("read workspace status: %w", err)
	}
	if status := statusOut; len(status) == 0 {
		return nil
	}

	if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("stage workspace changes: %w", err)
	}

	commitMsg := fmt.Sprintf("chore: relay session %d/%d", chatID, topicID)
	if err := git.GitRunCmdErr(ctx, workspaceDir, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("commit workspace changes: %w", err)
	}

	return nil
}

func (m *Manager) persistSessionRecord(ctx context.Context, ts *TopicSession, status string) error {
	if ts == nil {
		return fmt.Errorf("topic session is required")
	}
	if strings.TrimSpace(status) == "" {
		status = relaystate.SessionStatusActive
	}

	return m.sessionStore.Upsert(ctx, relaystate.SessionRecord{
		SessionID:    ts.sessionID,
		ChannelType:  ts.locator.ChannelType,
		AddressKey:   ts.locator.AddressKey,
		AddressJSON:  ts.locator.AddressJSON,
		AgentName:    ts.agentName,
		WorkspaceDir: ts.workspaceDir,
		BranchName:   ts.branchName,
		Status:       status,
	})
}
