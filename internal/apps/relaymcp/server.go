package relaymcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName     = "norma-relay"
	serverVersion  = "1.0.0"
	defaultAddress = "127.0.0.1:9090"
)

type ToolError struct {
	Operation string `json:"operation"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type ToolOutcome struct {
	OK    bool       `json:"ok"`
	Error *ToolError `json:"error,omitempty"`
}

func okOutcome() ToolOutcome {
	return ToolOutcome{OK: true}
}

func validationFailure(operation string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "validation_error", message)
}

func backendFailure(operation string, err error) (*mcp.CallToolResult, ToolOutcome) {
	return failure(operation, "backend_error", err.Error())
}

func failure(operation string, code string, message string) (*mcp.CallToolResult, ToolOutcome) {
	return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: message}},
		}, ToolOutcome{
			OK: false,
			Error: &ToolError{
				Operation: operation,
				Code:      code,
				Message:   message,
			},
		}
}

type RelayService interface {
	StartAgent(ctx context.Context, chatID int64, agentName string) (AgentInfo, error)
	StopAgent(ctx context.Context, sessionID string) error
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	GetSession(ctx context.Context, sessionID string) (AgentInfo, error)
}

type AgentInfo struct {
	ChannelType string
	AddressKey  string
	SessionID   string
	AgentName   string
	ChatID      int64
	TopicID     int
	WorkingDir  string
	Status      string
	Description string
	MCPServers  []string
}

func Run(ctx context.Context, svc RelayService) error {
	server, err := NewServer(svc)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func RunHTTP(ctx context.Context, svc RelayService, addr string) error {
	result, err := StartHTTPServer(ctx, svc, addr)
	if err != nil {
		return err
	}
	<-ctx.Done()
	return result.Close()
}

type HTTPServerResult struct {
	Addr  string
	Close func() error
}

func StartHTTPServer(ctx context.Context, svc RelayService, addr string) (*HTTPServerResult, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}
	address := strings.TrimSpace(addr)
	if address == "" {
		address = defaultAddress
	}

	getServer := func(_ *http.Request) *mcp.Server {
		server, err := NewServer(svc)
		if err != nil {
			return nil
		}
		return server
	}

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{})

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", address, err)
	}

	actualAddr := listener.Addr().String()
	httpServer := &http.Server{Handler: handler}

	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	go func() {
		_ = httpServer.Serve(listener)
	}()

	return &HTTPServerResult{
		Addr: actualAddr,
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

func NewServer(svc RelayService) (*mcp.Server, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    serverName,
			Version: serverVersion,
		},
		nil,
	)

	RegisterTools(server, svc)
	return server, nil
}

// RegisterTools adds relay agent-management MCP tools to an existing server.
func RegisterTools(server *mcp.Server, svc RelayService) {
	if server == nil || svc == nil {
		return
	}
	srv := &service{svc: svc}
	srv.registerTools(server)
}

type service struct {
	svc RelayService
}

func (s *service) registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.start_agent",
		Description: "Start a new relay agent session",
	}, s.startAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.stop_agent",
		Description: "Stop a running relay agent session",
	}, s.stopAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.list_agents",
		Description: "List all active relay agent sessions",
	}, s.listAgents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.get_agent",
		Description: "Get information about a specific relay agent session",
	}, s.getAgent)
}

type startAgentInput struct {
	ChatID    int64  `json:"chat_id,omitempty" jsonschema:"Telegram chat ID where topic should be created; optional when session_id is provided"`
	SessionID string `json:"session_id,omitempty" jsonschema:"existing relay session ID used to infer the current chat/channel context"`
	AgentName string `json:"agent_name" jsonschema:"name of the agent to start"`
}

type startAgentOutput struct {
	ToolOutcome
	ChannelType string   `json:"channel_type,omitempty"`
	AddressKey  string   `json:"address_key,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	TopicID     int      `json:"topic_id,omitempty"`
	ChatID      int64    `json:"chat_id,omitempty"`
	AgentName   string   `json:"agent_name,omitempty"`
	Description string   `json:"description,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
}

func (s *service) startAgent(ctx context.Context, _ *mcp.CallToolRequest, in startAgentInput) (*mcp.CallToolResult, startAgentOutput, error) {
	if strings.TrimSpace(in.AgentName) == "" {
		result, out := validationFailure("relay.agents.start_agent", "agent_name is required")
		return result, startAgentOutput{ToolOutcome: out}, nil
	}

	chatID := in.ChatID
	if chatID == 0 {
		if strings.TrimSpace(in.SessionID) == "" {
			result, out := validationFailure("relay.agents.start_agent", "chat_id or session_id is required")
			return result, startAgentOutput{ToolOutcome: out}, nil
		}
		parent, err := s.svc.GetSession(ctx, in.SessionID)
		if err != nil {
			result, out := backendFailure("relay.agents.start_agent", fmt.Errorf("resolve session context: %w", err))
			return result, startAgentOutput{ToolOutcome: out}, nil
		}
		chatID = parent.ChatID
		if chatID == 0 {
			result, out := validationFailure("relay.agents.start_agent", fmt.Sprintf("session %q has no chat context", in.SessionID))
			return result, startAgentOutput{ToolOutcome: out}, nil
		}
	}

	info, err := s.svc.StartAgent(ctx, chatID, in.AgentName)
	if err != nil {
		result, out := backendFailure("relay.agents.start_agent", err)
		return result, startAgentOutput{ToolOutcome: out}, nil
	}

	return nil, startAgentOutput{
		ToolOutcome: okOutcome(),
		ChannelType: info.ChannelType,
		AddressKey:  info.AddressKey,
		SessionID:   info.SessionID,
		TopicID:     info.TopicID,
		ChatID:      info.ChatID,
		AgentName:   info.AgentName,
		Description: info.Description,
		MCPServers:  info.MCPServers,
	}, nil
}

type stopAgentInput struct {
	SessionID string `json:"session_id" jsonschema:"session ID to stop"`
}

func (s *service) stopAgent(ctx context.Context, _ *mcp.CallToolRequest, in stopAgentInput) (*mcp.CallToolResult, ToolOutcome, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure("relay.agents.stop_agent", "session_id is required")
		return result, out, nil
	}

	if err := s.svc.StopAgent(ctx, in.SessionID); err != nil {
		result, out := backendFailure("relay.agents.stop_agent", err)
		return result, out, nil
	}

	return nil, okOutcome(), nil
}

type listAgentsOutput struct {
	ToolOutcome
	Agents []AgentInfo `json:"agents,omitempty"`
}

func (s *service) listAgents(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listAgentsOutput, error) {
	agents, err := s.svc.ListAgents(ctx)
	if err != nil {
		result, out := backendFailure("relay.agents.list_agents", err)
		return result, listAgentsOutput{ToolOutcome: out}, nil
	}

	return nil, listAgentsOutput{
		ToolOutcome: okOutcome(),
		Agents:      agents,
	}, nil
}

type getAgentInput struct {
	SessionID string `json:"session_id" jsonschema:"session ID to retrieve"`
}

type getAgentOutput struct {
	ToolOutcome
	Agent *AgentInfo `json:"agent,omitempty"`
}

func (s *service) getAgent(ctx context.Context, _ *mcp.CallToolRequest, in getAgentInput) (*mcp.CallToolResult, getAgentOutput, error) {
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure("relay.agents.get_agent", "session_id is required")
		return result, getAgentOutput{ToolOutcome: out}, nil
	}

	agent, err := s.svc.GetSession(ctx, in.SessionID)
	if err != nil {
		result, out := validationFailure("relay.agents.get_agent", fmt.Sprintf("session not found: %v", err))
		return result, getAgentOutput{ToolOutcome: out}, nil
	}

	return nil, getAgentOutput{
		ToolOutcome: okOutcome(),
		Agent:       &agent,
	}, nil
}
