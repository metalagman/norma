package session

import (
	"context"
	"fmt"

	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaywelcome "github.com/normahq/norma/internal/apps/relay/welcome"
	"github.com/normahq/norma/internal/apps/relaymcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type relayChannelRuntime interface {
	CreateTopicLocator(ctx context.Context, chatID int64, topicName string) (SessionLocator, error)
	Close(ctx context.Context, locator SessionLocator) error
	SendMarkdown(ctx context.Context, locator SessionLocator, text string) error
}

type relayMCPServer struct {
	manager   *Manager
	channel   relayChannelRuntime
	messenger *messenger.Messenger
	logger    zerolog.Logger
}

// NewRelayMCPServer wraps a session Manager as a RelayService.
func NewRelayMCPServer(manager *Manager, channel relayChannelRuntime, msg *messenger.Messenger) relaymcp.RelayService {
	return &relayMCPServer{
		manager:   manager,
		channel:   channel,
		messenger: msg,
		logger:    log.With().Str("component", "relay.mcp").Logger(),
	}
}

func (s *relayMCPServer) StartAgent(ctx context.Context, chatID int64, agentName string) (relaymcp.AgentInfo, error) {
	s.logger.Info().
		Int64("chat_id", chatID).
		Str("agent", agentName).
		Msg("MCP: StartAgent called")

	if err := s.manager.ValidateAgent(agentName); err != nil {
		return relaymcp.AgentInfo{}, fmt.Errorf("agent %q not available: %w", agentName, err)
	}

	locator, err := s.channel.CreateTopicLocator(ctx, chatID, fmt.Sprintf("Relay: %s", agentName))
	if err != nil {
		s.logger.Error().
			Err(err).
			Int64("chat_id", chatID).
			Str("agent", agentName).
			Msg("MCP: StartAgent failed")
		return relaymcp.AgentInfo{}, err
	}
	if err := s.manager.CreateSession(ctx, locator, agentName); err != nil {
		_ = s.channel.Close(ctx, locator)
		return relaymcp.AgentInfo{}, err
	}

	address, ok, err := locator.TelegramAddress()
	if err != nil {
		return relaymcp.AgentInfo{}, err
	}
	if !ok {
		return relaymcp.AgentInfo{}, fmt.Errorf("unsupported channel type %q", locator.ChannelType)
	}
	agentDesc, mcpServers := s.manager.GetAgentInfo(agentName)

	if s.messenger != nil {
		welcomeMsg := relaywelcome.BuildAgentWelcomeMessage(agentName, locator.SessionID, agentDesc, mcpServers)
		if sendErr := s.channel.SendMarkdown(ctx, locator, welcomeMsg); sendErr != nil {
			s.logger.Warn().
				Err(sendErr).
				Int64("chat_id", chatID).
				Int("topic_id", address.TopicID).
				Str("agent", agentName).
				Str("session_id", locator.SessionID).
				Msg("MCP: failed to send welcome message to topic")
		}
	}

	s.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", address.TopicID).
		Str("agent", agentName).
		Str("session_id", locator.SessionID).
		Msg("MCP: StartAgent succeeded")

	return relaymcp.AgentInfo{
		ChannelType: locator.ChannelType,
		AddressKey:  locator.AddressKey,
		SessionID:   locator.SessionID,
		AgentName:   agentName,
		ChatID:      chatID,
		TopicID:     address.TopicID,
		Description: agentDesc,
		MCPServers:  mcpServers,
	}, nil
}

func (s *relayMCPServer) StopAgent(ctx context.Context, sessionID string) error {
	s.logger.Info().Str("session_id", sessionID).Msg("MCP: StopAgent called")

	if err := s.manager.StopSessionByID(ctx, sessionID); err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: StopAgent failed")
		return err
	}

	s.logger.Info().Str("session_id", sessionID).Msg("MCP: StopAgent succeeded")
	return nil
}

func (s *relayMCPServer) ListAgents(ctx context.Context) ([]relaymcp.AgentInfo, error) {
	infos, err := s.manager.ListSessionInfos(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]relaymcp.AgentInfo, 0, len(infos))

	s.logger.Debug().Int("count", len(infos)).Msg("MCP: ListAgents called")

	for _, info := range infos {
		out = append(out, relaymcp.AgentInfo{
			ChannelType: info.ChannelType,
			AddressKey:  info.Locator.AddressKey,
			SessionID:  info.SessionID,
			AgentName:  info.AgentName,
			ChatID:     info.ChatID,
			TopicID:    info.TopicID,
			WorkingDir: info.WorkspaceDir,
			Status:     info.Status,
		})
	}
	return out, nil
}

func (s *relayMCPServer) GetSession(ctx context.Context, sessionID string) (relaymcp.AgentInfo, error) {
	s.logger.Debug().Str("session_id", sessionID).Msg("MCP: GetSession called")

	info, err := s.manager.GetSessionInfo(ctx, sessionID)
	if err != nil {
		s.logger.Warn().Err(err).Str("session_id", sessionID).Msg("MCP: GetSession failed - session not found")
		return relaymcp.AgentInfo{}, err
	}

	return relaymcp.AgentInfo{
		ChannelType: info.ChannelType,
		AddressKey:  info.Locator.AddressKey,
		SessionID:  info.SessionID,
		AgentName:  info.AgentName,
		ChatID:     info.ChatID,
		TopicID:    info.TopicID,
		WorkingDir: info.WorkspaceDir,
		Status:     info.Status,
	}, nil
}
