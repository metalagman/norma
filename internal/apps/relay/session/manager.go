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
	"github.com/tgbotkit/client"
	"go.uber.org/fx"
)

const sessionIDPrefix = "relay"

const cleanupTimeout = 10 * time.Second

// Manager manages per-topic ADK agent sessions and persists session metadata.
type Manager struct {
	agentBuilder      *agent.Builder
	relayMCPServerIDs []string
	workingDir        string
	tgClient          client.ClientWithResponsesInterface
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
	TGClient          client.ClientWithResponsesInterface
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
		tgClient:          p.TGClient,
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

func (m *Manager) sessionID(chatID int64, topicID int) string {
	return fmt.Sprintf("%s-%d-%d", sessionIDPrefix, chatID, topicID)
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

// CreateSession builds an agent for the given topic and stores it in memory.
func (m *Manager) CreateSession(ctx context.Context, chatID int64, topicID int, agentName string) error {
	sessionID := m.sessionID(chatID, topicID)

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Msg("creating session")

	m.mu.Lock()
	if _, exists := m.sessions[sessionID]; exists {
		m.mu.Unlock()
		m.logger.Warn().Str("session_id", sessionID).Msg("session already exists")
		return fmt.Errorf("session already exists for topic %d", topicID)
	}
	m.mu.Unlock()

	branchName := ""
	workspaceDir := m.workingDir
	if m.workspaceEnabled {
		branchName = m.SessionBranchName(sessionID)
		var err error
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
		Msg("session created successfully")

	return nil
}

// CreateTopicSession creates a Telegram forum topic and an agent session for it.
// It first validates the agent can be built before creating the topic to avoid orphaned topics.
func (m *Manager) CreateTopicSession(ctx context.Context, chatID int64, agentName string) (string, int, error) {
	m.logger.Info().
		Int64("chat_id", chatID).
		Str("agent", agentName).
		Msg("creating topic session")

	// First validate agent can be built - this checks config without creating anything
	if err := m.ValidateAgent(agentName); err != nil {
		m.logger.Error().Err(err).Str("agent", agentName).Msg("agent validation failed, not creating topic")
		return "", 0, fmt.Errorf("agent %q not available: %w", agentName, err)
	}

	// Agent validated, now create the topic
	topicName := fmt.Sprintf("Relay: %s", agentName)
	createTopicResp, err := m.tgClient.CreateForumTopicWithResponse(ctx, client.CreateForumTopicJSONRequestBody{
		ChatId: chatID,
		Name:   topicName,
	})
	if err != nil {
		m.logger.Error().Err(err).Int64("chat_id", chatID).Msg("failed to create forum topic")
		return "", 0, fmt.Errorf("creating forum topic: %w", err)
	}
	if createTopicResp.JSON200 == nil {
		m.logger.Error().
			Int64("chat_id", chatID).
			Str("status", createTopicResp.Status()).
			Msg("forum topic creation returned non-200")
		return "", 0, fmt.Errorf("failed to create forum topic: %s", createTopicResp.Status())
	}

	topic := createTopicResp.JSON200.Result
	topicID := topic.MessageThreadId

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Msg("forum topic created, creating agent session")

	if err := m.CreateSession(ctx, chatID, topicID, agentName); err != nil {
		m.logger.Error().Err(err).Int64("chat_id", chatID).Int("topic_id", topicID).Msg("failed to create session, cleaning up topic")
		m.closeTopic(ctx, chatID, topicID)
		return "", 0, fmt.Errorf("creating agent session: %w", err)
	}

	sessionID := m.sessionID(chatID, topicID)
	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Msg("topic session created successfully")

	return sessionID, topicID, nil
}

// GetSession returns the in-memory session for the given chat/topic.
func (m *Manager) GetSession(chatID int64, topicID int) (*TopicSession, error) {
	sessionID := m.sessionID(chatID, topicID)

	m.mu.RLock()
	ts := m.sessions[sessionID]
	m.mu.RUnlock()

	if ts == nil {
		m.logger.Debug().
			Int64("chat_id", chatID).
			Int("topic_id", topicID).
			Str("session_id", sessionID).
			Int("active_sessions", len(m.sessions)).
			Msg("session not found")
		return nil, fmt.Errorf("no session for topic %d", topicID)
	}

	return ts, nil
}

// EnsureSession returns the existing session or creates a new one if it doesn't exist.
// For topic 0 (main orchestrator), it creates the session without a forum topic.
func (m *Manager) EnsureSession(ctx context.Context, chatID int64, topicID int, agentName string) (*TopicSession, error) {
	sessionID := m.sessionID(chatID, topicID)

	m.mu.RLock()
	ts := m.sessions[sessionID]
	m.mu.RUnlock()

	if ts != nil {
		m.logger.Debug().
			Int64("chat_id", chatID).
			Int("topic_id", topicID).
			Str("session_id", sessionID).
			Msg("returning existing session")
		return ts, nil
	}

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Msg("creating new session via EnsureSession")

	// Create new session
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if ts = m.sessions[sessionID]; ts != nil {
		return ts, nil
	}

	branchName := ""
	workspaceDir := m.workingDir
	if m.workspaceEnabled {
		branchName = m.SessionBranchName(sessionID)
		var err error
		workspaceDir, err = m.workspaces.EnsureWorkspace(ctx, sessionID, branchName, "")
		if err != nil {
			m.logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to create workspace")
			return nil, fmt.Errorf("create workspace: %w", err)
		}
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
		return nil, err
	}

	ts = &TopicSession{
		sessionID:    sessionID,
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
		return nil, fmt.Errorf("persist session metadata: %w", err)
	}

	m.sessions[sessionID] = ts

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Msg("session created via EnsureSession")

	return ts, nil
}

// RestoreSession restores a topic session from persisted metadata when it is not active in memory.
func (m *Manager) RestoreSession(ctx context.Context, chatID int64, topicID int) (*TopicSession, error) {
	sessionID := m.sessionID(chatID, topicID)

	m.mu.RLock()
	if ts := m.sessions[sessionID]; ts != nil {
		m.mu.RUnlock()
		return ts, nil
	}
	m.mu.RUnlock()

	record, ok, err := m.sessionStore.GetByChatTopic(ctx, chatID, topicID)
	if err != nil {
		return nil, fmt.Errorf("read session metadata: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("no persisted session for topic %d", topicID)
	}
	if strings.TrimSpace(record.Status) != "" && record.Status != relaystate.SessionStatusActive {
		return nil, fmt.Errorf("persisted session for topic %d is not active", topicID)
	}
	if strings.TrimSpace(record.AgentName) == "" {
		return nil, fmt.Errorf("persisted session for topic %d has empty agent name", topicID)
	}

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("session_id", sessionID).
		Str("agent", record.AgentName).
		Msg("restoring session from persisted metadata")

	return m.EnsureSession(ctx, chatID, topicID, record.AgentName)
}

// StopSession removes a session from memory and cleans up.
func (m *Manager) StopSession(chatID int64, topicID int) {
	sessionID := m.sessionID(chatID, topicID)

	m.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("session_id", sessionID).
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

// CloseTopic closes the Telegram forum topic via API.
func (m *Manager) CloseTopic(ctx context.Context, chatID int64, topicID int) {
	m.closeTopic(ctx, chatID, topicID)
}

// ListSessions returns info about all active sessions.
func (m *Manager) ListSessions() []TopicSessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]TopicSessionInfo, 0, len(m.sessions))
	for _, ts := range m.sessions {
		out = append(out, TopicSessionInfo{
			SessionID:    ts.sessionID,
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

func (m *Manager) closeTopic(ctx context.Context, chatID int64, topicID int) {
	m.logger.Debug().Int64("chat_id", chatID).Int("topic_id", topicID).Msg("closing forum topic")

	closeResp, err := m.tgClient.CloseForumTopicWithResponse(ctx, client.CloseForumTopicJSONRequestBody{
		ChatId:          chatID,
		MessageThreadId: topicID,
	})
	if err != nil {
		m.logger.Warn().Err(err).Int64("chat_id", chatID).Int("topic_id", topicID).Msg("failed to close forum topic")
		return
	}
	if closeResp.JSON200 == nil {
		m.logger.Warn().Int64("chat_id", chatID).Int("topic_id", topicID).Str("status", closeResp.Status()).Msg("failed to close forum topic")
	}
}

func (m *Manager) CommitWorkspace(ctx context.Context, chatID int64, topicID int) error {
	if !m.workspaceEnabled {
		return fmt.Errorf("workspace mode is disabled")
	}

	sessionID := m.sessionID(chatID, topicID)

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
		ChatID:       ts.chatID,
		TopicID:      ts.topicID,
		AgentName:    ts.agentName,
		WorkspaceDir: ts.workspaceDir,
		BranchName:   ts.branchName,
		Status:       status,
	})
}
