package session

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

// TopicSession represents a single Telegram topic's ADK agent session.
type TopicSession struct {
	sessionID    string
	userID       string
	locator      SessionLocator
	topicID      int
	agentName    string
	agent        agent.Agent
	runner       *runner.Runner
	sessionSvc   session.Service
	sess         session.Session
	chatID       int64
	workspaceDir string
	branchName   string
	relayMCPID   string
}

func (s *TopicSession) GetRunner() *runner.Runner {
	return s.runner
}

func (s *TopicSession) GetSessionID() string {
	return s.sessionID
}

func (s *TopicSession) GetUserID() string {
	return s.userID
}

func (s *TopicSession) GetWorkspaceDir() string {
	return s.workspaceDir
}

func (s *TopicSession) GetAgentName() string {
	return s.agentName
}

func (s *TopicSession) GetLocator() SessionLocator {
	return s.locator
}
