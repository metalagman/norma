package session

import (
	"context"

	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaywelcome "github.com/normahq/norma/internal/apps/relay/welcome"
	"github.com/normahq/norma/internal/apps/relaymcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type relayMCPServer struct {
	manager   *Manager
	messenger *messenger.Messenger
	logger    zerolog.Logger
}

// NewRelayMCPServer wraps a session Manager as a RelayService.
func NewRelayMCPServer(manager *Manager, msg *messenger.Messenger) relaymcp.RelayService {
	return &relayMCPServer{
		manager:   manager,
		messenger: msg,
		logger:    log.With().Str("component", "relay.mcp").Logger(),
	}
}

func (s *relayMCPServer) StartAgent(ctx context.Context, chatID int64, agentName string) (relaymcp.AgentInfo, error) {
	s.logger.Info().
		Int64("chat_id", chatID).
		Str("agent", agentName).
		Msg("MCP: StartAgent called")

	sessionID, topicID, err := s.manager.CreateTopicSession(ctx, chatID, agentName)
	if err != nil {
		s.logger.Error().
			Err(err).
			Int64("chat_id", chatID).
			Str("agent", agentName).
			Msg("MCP: StartAgent failed")
		return relaymcp.AgentInfo{}, err
	}
	agentDesc, mcpServers := s.manager.GetAgentInfo(agentName)

	if s.messenger != nil {
		welcomeMsg := relaywelcome.BuildAgentWelcomeMessage(agentName, sessionID, agentDesc, mcpServers)
		if sendErr := s.messenger.SendMarkdown(ctx, chatID, welcomeMsg, topicID); sendErr != nil {
			s.logger.Warn().
				Err(sendErr).
				Int64("chat_id", chatID).
				Int("topic_id", topicID).
				Str("agent", agentName).
				Str("session_id", sessionID).
				Msg("MCP: failed to send welcome message to topic")
		}
	}

	s.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("agent", agentName).
		Str("session_id", sessionID).
		Msg("MCP: StartAgent succeeded")

	return relaymcp.AgentInfo{
		SessionID:   sessionID,
		AgentName:   agentName,
		ChatID:      chatID,
		TopicID:     topicID,
		Description: agentDesc,
		MCPServers:  mcpServers,
	}, nil
}

func (s *relayMCPServer) StopAgent(_ context.Context, sessionID string) error {
	s.logger.Info().Str("session_id", sessionID).Msg("MCP: StopAgent called")

	ts, err := s.findBySessionID(sessionID)
	if err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: StopAgent failed - session not found")
		return err
	}

	s.manager.StopSession(ts.GetLocator())
	s.logger.Info().Str("session_id", sessionID).Msg("MCP: StopAgent succeeded")
	return nil
}

func (s *relayMCPServer) ListAgents(_ context.Context) ([]relaymcp.AgentInfo, error) {
	infos := s.manager.ListSessions()
	out := make([]relaymcp.AgentInfo, 0, len(infos))

	s.logger.Debug().Int("count", len(infos)).Msg("MCP: ListAgents called")

	for _, info := range infos {
		out = append(out, relaymcp.AgentInfo{
			SessionID:  info.SessionID,
			AgentName:  info.AgentName,
			ChatID:     info.ChatID,
			TopicID:    info.TopicID,
			WorkingDir: info.WorkspaceDir,
			Status:     "active",
		})
	}
	return out, nil
}

func (s *relayMCPServer) GetSession(_ context.Context, sessionID string) (relaymcp.AgentInfo, error) {
	s.logger.Debug().Str("session_id", sessionID).Msg("MCP: GetSession called")

	ts, err := s.findBySessionID(sessionID)
	if err != nil {
		s.logger.Warn().Err(err).Str("session_id", sessionID).Msg("MCP: GetSession failed - session not found")
		return relaymcp.AgentInfo{}, err
	}

	return relaymcp.AgentInfo{
		SessionID:  ts.sessionID,
		AgentName:  ts.agentName,
		ChatID:     ts.chatID,
		TopicID:    ts.topicID,
		WorkingDir: ts.workspaceDir,
		Status:     "active",
	}, nil
}

func (s *relayMCPServer) findBySessionID(sessionID string) (*TopicSession, error) {
	return s.manager.FindSessionByID(sessionID)
}
