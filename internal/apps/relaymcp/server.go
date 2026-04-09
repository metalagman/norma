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

const serverInstructions = `Use this server to manage relay agent sessions.

- relay.agents.start_agent creates a new relay session for a configured agent.
- Prefer passing session_id so the server can infer the current channel context.
- chat_id is a Telegram-specific override for starting a session in a specific chat.
- list_agents returns both active sessions and persisted restorable sessions.
- get_agent and stop_agent operate on a relay session_id.`

type ToolError struct {
	Operation string `json:"operation" jsonschema:"tool name that produced the error"`
	Code      string `json:"code" jsonschema:"stable machine-readable error code"`
	Message   string `json:"message" jsonschema:"human-readable error message"`
}

type ToolOutcome struct {
	OK    bool       `json:"ok" jsonschema:"true when the tool completed successfully"`
	Error *ToolError `json:"error,omitempty" jsonschema:"error details when ok is false"`
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
	ChannelType string   `json:"channel_type,omitempty" jsonschema:"channel type that owns the session, for example telegram"`
	AddressKey  string   `json:"address_key,omitempty" jsonschema:"channel-specific address key used internally to identify the session context"`
	SessionID   string   `json:"session_id,omitempty" jsonschema:"relay session ID"`
	AgentName   string   `json:"agent_name,omitempty" jsonschema:"configured agent name running in the session"`
	ChatID      int64    `json:"chat_id,omitempty" jsonschema:"Telegram chat ID when the session belongs to Telegram; omitted for other channels"`
	TopicID     int      `json:"topic_id,omitempty" jsonschema:"Telegram topic ID when the session belongs to a forum topic; root sessions use 0"`
	WorkingDir  string   `json:"working_dir,omitempty" jsonschema:"working directory assigned to the session"`
	Status      string   `json:"status,omitempty" jsonschema:"session lifecycle status such as active or persisted"`
	Description string   `json:"description,omitempty" jsonschema:"human-readable configured agent description"`
	MCPServers  []string `json:"mcp_servers,omitempty" jsonschema:"MCP server IDs mounted into the session"`
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
		&mcp.ServerOptions{Instructions: serverInstructions},
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
		Description: "Start a new relay agent session for a configured agent. Prefer session_id to reuse the current channel context; chat_id is a Telegram-only override.",
	}, s.startAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.stop_agent",
		Description: "Stop one relay agent session by session_id.",
	}, s.stopAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.list_agents",
		Description: "List relay agent sessions, including active sessions and persisted restorable sessions.",
	}, s.listAgents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "relay.agents.get_agent",
		Description: "Get one relay agent session object by session_id, including channel context, status, and mounted MCP servers.",
	}, s.getAgent)
}

type startAgentInput struct {
	ChatID    int64  `json:"chat_id,omitempty" jsonschema:"Telegram chat ID where a new topic should be created; optional when session_id is provided"`
	SessionID string `json:"session_id,omitempty" jsonschema:"existing relay session ID used to infer the current channel context"`
	AgentName string `json:"agent_name" jsonschema:"configured agent name to start"`
}

type startAgentOutput struct {
	ToolOutcome
	ChannelType string   `json:"channel_type,omitempty" jsonschema:"channel type that owns the new session"`
	AddressKey  string   `json:"address_key,omitempty" jsonschema:"channel-specific address key for the new session"`
	SessionID   string   `json:"session_id,omitempty" jsonschema:"new relay session ID"`
	TopicID     int      `json:"topic_id,omitempty" jsonschema:"Telegram topic ID when a new forum topic was created"`
	ChatID      int64    `json:"chat_id,omitempty" jsonschema:"Telegram chat ID when applicable"`
	AgentName   string   `json:"agent_name,omitempty" jsonschema:"configured agent name that was started"`
	Description string   `json:"description,omitempty" jsonschema:"human-readable configured agent description"`
	MCPServers  []string `json:"mcp_servers,omitempty" jsonschema:"MCP server IDs mounted into the new session"`
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
	SessionID string `json:"session_id" jsonschema:"relay session ID to stop"`
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
	Agents []AgentInfo `json:"agents,omitempty" jsonschema:"relay session objects for active and persisted sessions"`
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
	SessionID string `json:"session_id" jsonschema:"relay session ID to retrieve"`
}

type getAgentOutput struct {
	ToolOutcome
	Agent *AgentInfo `json:"agent,omitempty" jsonschema:"relay session object for the requested session"`
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
