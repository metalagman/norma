package session

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/normahq/norma/internal/apps/relay/agent"
	relaystate "github.com/normahq/norma/internal/apps/relay/state"
	"github.com/normahq/norma/internal/git"
	"github.com/rs/zerolog"
	"go.uber.org/fx"
)

const cleanupTimeout = 10 * time.Second

// Manager manages relay ADK sessions and persists session metadata.
type Manager struct {
	agentBuilder      *agent.Builder
	relayMCPServerIDs []string
	workingDir        string
	workspaces        *agent.WorkspaceManager
	workspaceEnabled  bool
	sessionStore      relaystate.SessionStore
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
	AgentBuilder      *agent.Builder
	RelayMCPServerIDs []string `name:"relay_mcp_servers"`
	WorkingDir        string
	WorkspaceEnabled  bool   `name:"relay_workspace_enabled"`
	WorkspaceBaseRef  string `name:"relay_workspace_base_branch"`
	StateProvider     relaystate.Provider
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
		workingDir:        p.WorkingDir,
		workspaces:        agent.NewWorkspaceManager(p.WorkingDir, p.WorkspaceBaseRef),
		workspaceEnabled:  p.WorkspaceEnabled,
		sessionStore:      p.StateProvider.Sessions(),
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
	return m.agentBuilder.ValidateAgent(agentName)
}

// GetAgentInfo returns the description and list of MCP server names for an agent.
func (m *Manager) GetAgentInfo(agentName string) (string, []string) {
	description, mcpServers := m.agentBuilder.GetAgentInfo(agentName)
	return description, mergeUniqueStringIDs(mcpServers, m.relayMCPServerIDs)
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
	if len(m.relayMCPServerIDs) == 0 {
		return nil
	}
	return append([]string(nil), m.relayMCPServerIDs...)
}

// CreateSession builds an agent for the given locator and stores it in memory.
func (m *Manager) CreateSession(ctx context.Context, locator SessionLocator, agentName string) error {
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

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
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

	built, err := m.agentBuilder.BuildWithMCPServerIDs(
		m.rootCtx,
		sessionID,
		chatID,
		topicID,
		agentName,
		workspaceDir,
		m.extraMCPServerIDs(),
	)
	if err != nil {
		m.logger.Error().Err(err).Str("session_id", sessionID).Str("agent", agentName).Msg("failed to build agent")
		if m.workspaceEnabled {
			_ = m.workspaces.CleanupWorkspace(ctx, workspaceDir)
		}
		return err
	}

	ts := &TopicSession{
		sessionID:    sessionID,
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
	}

	if err := m.persistSessionRecord(ctx, ts, relaystate.SessionStatusActive); err != nil {
		if closer, ok := ts.agent.(io.Closer); ok {
			_ = closer.Close()
		}
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

// FindSessionByID returns the in-memory session with the given session ID.
func (m *Manager) FindSessionByID(sessionID string) (*TopicSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts := m.sessions[strings.TrimSpace(sessionID)]
	if ts == nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return ts, nil
}

// EnsureSession returns the existing session or creates a new one if it doesn't exist.
func (m *Manager) EnsureSession(ctx context.Context, locator SessionLocator, agentName string) (*TopicSession, error) {
	sessionID := strings.TrimSpace(locator.SessionID)

	m.mu.RLock()
	ts := m.sessions[sessionID]
	m.mu.RUnlock()

	if ts != nil {
		m.logger.Debug().Str("session_id", sessionID).Msg("returning existing session")
		return ts, nil
	}

	if err := m.CreateSession(ctx, locator, agentName); err != nil {
		return nil, err
	}
	return m.GetSession(locator)
}

// EnsureTelegramSession returns the existing Telegram session or creates a new one.
func (m *Manager) EnsureTelegramSession(ctx context.Context, chatID int64, topicID int, agentName string) (*TopicSession, error) {
	return m.EnsureSession(ctx, NewTelegramSessionLocator(chatID, topicID), agentName)
}

// RestoreSession restores a session from persisted metadata when it is not active in memory.
func (m *Manager) RestoreSession(ctx context.Context, locator SessionLocator) (*TopicSession, error) {
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
		return nil, fmt.Errorf("no persisted session for %s", locator.AddressKey)
	}
	if strings.TrimSpace(record.Status) != "" && record.Status != relaystate.SessionStatusActive {
		return nil, fmt.Errorf("persisted session for %s is not active", locator.AddressKey)
	}
	if strings.TrimSpace(record.AgentName) == "" {
		return nil, fmt.Errorf("persisted session for %s has empty agent name", locator.AddressKey)
	}

	recordLocator, err := LocatorFromRecord(record)
	if err != nil {
		return nil, fmt.Errorf("decode persisted session locator: %w", err)
	}

	m.logger.Info().
		Str("session_id", sessionID).
		Str("channel_type", recordLocator.ChannelType).
		Str("address_key", recordLocator.AddressKey).
		Str("agent", record.AgentName).
		Msg("restoring session from persisted metadata")

	return m.EnsureSession(ctx, recordLocator, record.AgentName)
}

// RestoreTelegramSession restores a Telegram session from persisted metadata.
func (m *Manager) RestoreTelegramSession(ctx context.Context, chatID int64, topicID int) (*TopicSession, error) {
	return m.RestoreSession(ctx, NewTelegramSessionLocator(chatID, topicID))
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
		out = append(out, TopicSessionInfo{
			SessionID:    ts.sessionID,
			ChannelType:  ts.locator.ChannelType,
			AgentName:    ts.agentName,
			ChatID:       ts.chatID,
			TopicID:      ts.topicID,
			WorkspaceDir: ts.workspaceDir,
			BranchName:   ts.branchName,
		})
	}
	return out
}

type TopicSessionInfo struct {
	SessionID    string
	ChannelType  string
	AgentName    string
	ChatID       int64
	TopicID      int
	WorkspaceDir string
	BranchName   string
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
