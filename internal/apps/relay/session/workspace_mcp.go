package session

import (
	"context"
	"fmt"

	"github.com/normahq/norma/internal/apps/workspacemcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type workspaceMCPServer struct {
	manager *Manager
	logger  zerolog.Logger
}

// NewWorkspaceMCPServer wraps a session Manager as a WorkspaceService.
func NewWorkspaceMCPServer(manager *Manager) workspacemcp.WorkspaceService {
	return &workspaceMCPServer{
		manager: manager,
		logger:  log.With().Str("component", "workspace.mcp").Logger(),
	}
}

func (s *workspaceMCPServer) Import(ctx context.Context, sessionID string) error {
	s.logger.Info().Str("session_id", sessionID).Msg("MCP: Import called")
	if !s.manager.workspaceEnabled {
		return fmt.Errorf("workspace mode is disabled")
	}

	ts, err := s.findBySessionID(sessionID)
	if err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: Import failed — session not found")
		return err
	}

	if err := s.manager.workspaces.Import(ctx, ts.workspaceDir); err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: Import failed")
		return err
	}

	s.logger.Info().Str("session_id", sessionID).Msg("MCP: Import succeeded")
	return nil
}

func (s *workspaceMCPServer) Export(ctx context.Context, sessionID string, commitMessage string) error {
	s.logger.Info().Str("session_id", sessionID).Str("message", commitMessage).Msg("MCP: Export called")
	if !s.manager.workspaceEnabled {
		return fmt.Errorf("workspace mode is disabled")
	}

	ts, err := s.findBySessionID(sessionID)
	if err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: Export failed — session not found")
		return err
	}

	if err := s.manager.workspaces.Export(ctx, ts.workspaceDir, ts.branchName, commitMessage); err != nil {
		s.logger.Error().Err(err).Str("session_id", sessionID).Msg("MCP: Export failed")
		return err
	}

	s.logger.Info().Str("session_id", sessionID).Msg("MCP: Export succeeded")
	return nil
}

func (s *workspaceMCPServer) findBySessionID(sessionID string) (*TopicSession, error) {
	return s.manager.FindSessionByID(sessionID)
}
