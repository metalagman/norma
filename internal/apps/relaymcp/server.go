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
	// CallerSessionIDHeader binds a relay MCP request to the caller relay session.
	CallerSessionIDHeader = "X-Norma-Relay-Caller-Session-ID"
)

const (
	toolAgentsStart     = "relay.agents.start"
	toolAgentsStop      = "relay.agents.stop"
	toolAgentsStopAlias = "relay.agents.stop_agent"
	toolAgentsRestart   = "relay.agents.restart"
	toolAgentsList      = "relay.agents.list"
	toolAgentsListAlias = "relay.agents.list_agents"
	toolAgentsGet       = "relay.agents.get"
	toolAgentsGetAlias  = "relay.agents.get_agent"
)

const serverInstructions = `Use this server to manage relay agent sessions.

- relay.agents.start creates a new relay session for a configured agent.
- When this server is mounted for an existing relay session, start uses the current caller session context automatically.
- External callers can provide locator.channel_type plus locator.address to target a specific channel context.
- relay.agents.list returns both active sessions and persisted restorable sessions.
- relay.agents.get and relay.agents.stop operate on a relay session_id.
- Backward-compatible aliases remain available: relay.agents.list_agents, relay.agents.get_agent, relay.agents.stop_agent.`

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
	StartAgent(ctx context.Context, req StartRequest) (AgentInfo, error)
	StopAgent(ctx context.Context, sessionID string) error
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	GetSession(ctx context.Context, sessionID string) (AgentInfo, error)
}

type StartLocator struct {
	ChannelType string         `json:"channel_type,omitempty" jsonschema:"channel type that should own the new session, for example telegram"`
	Address     map[string]any `json:"address,omitempty" jsonschema:"channel-specific address object used to target the channel context, for example {\"chat_id\":123} for Telegram"`
}

type StartRequest struct {
	AgentName       string        `json:"agent_name"`
	CallerSessionID string        `json:"caller_session_id,omitempty"`
	Locator         *StartLocator `json:"locator,omitempty"`
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
		Name:        toolAgentsStart,
		Description: "Start a new relay agent session for a configured agent. The server uses the current caller session context automatically when available; external callers can provide locator.channel_type and locator.address instead.",
	}, s.startAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsStop,
		Description: "Stop one relay agent session by session_id.",
	}, s.stopAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsStopAlias,
		Description: "Deprecated alias of relay.agents.stop. Stop one relay agent session by session_id.",
	}, s.stopAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsRestart,
		Description: "Restart an existing relay agent session by session_id. The session is stopped and recreated with the same agent and channel context.",
	}, s.restartAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsList,
		Description: "List relay agent sessions, including active sessions and persisted restorable sessions.",
	}, s.listAgents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsListAlias,
		Description: "Deprecated alias of relay.agents.list. List relay agent sessions, including active sessions and persisted restorable sessions.",
	}, s.listAgents)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsGet,
		Description: "Get one relay agent session object by session_id, including channel context, status, and mounted MCP servers.",
	}, s.getAgent)
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolAgentsGetAlias,
		Description: "Deprecated alias of relay.agents.get. Get one relay agent session object by session_id, including channel context, status, and mounted MCP servers.",
	}, s.getAgent)
}

type startAgentInput struct {
	AgentName string        `json:"agent_name" jsonschema:"configured agent name to start"`
	Locator   *StartLocator `json:"locator,omitempty" jsonschema:"optional explicit channel locator for external callers; omit it when this relay MCP server is already bound to a caller session"`
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

func operationName(req *mcp.CallToolRequest, fallback string) string {
	if req == nil || req.Params == nil {
		return fallback
	}
	name := strings.TrimSpace(req.Params.Name)
	if name == "" {
		return fallback
	}
	return name
}

func callerSessionID(req *mcp.CallToolRequest) string {
	if req == nil || req.GetExtra() == nil || req.GetExtra().Header == nil {
		return ""
	}
	return strings.TrimSpace(req.GetExtra().Header.Get(CallerSessionIDHeader))
}

func (s *service) startAgent(ctx context.Context, req *mcp.CallToolRequest, in startAgentInput) (*mcp.CallToolResult, startAgentOutput, error) {
	operation := operationName(req, toolAgentsStart)
	if strings.TrimSpace(in.AgentName) == "" {
		result, out := validationFailure(operation, "agent_name is required")
		return result, startAgentOutput{ToolOutcome: out}, nil
	}
	if in.Locator == nil && callerSessionID(req) == "" {
		result, out := validationFailure(operation, "locator is required unless this relay MCP server is already bound to a caller session")
		return result, startAgentOutput{ToolOutcome: out}, nil
	}

	info, err := s.svc.StartAgent(ctx, StartRequest{
		AgentName:       in.AgentName,
		CallerSessionID: callerSessionID(req),
		Locator:         in.Locator,
	})
	if err != nil {
		result, out := backendFailure(operation, err)
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

func (s *service) stopAgent(ctx context.Context, req *mcp.CallToolRequest, in stopAgentInput) (*mcp.CallToolResult, ToolOutcome, error) {
	operation := operationName(req, toolAgentsStop)
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure(operation, "session_id is required")
		return result, out, nil
	}

	if err := s.svc.StopAgent(ctx, in.SessionID); err != nil {
		result, out := backendFailure(operation, err)
		return result, out, nil
	}

	return nil, okOutcome(), nil
}

type restartAgentInput struct {
	SessionID string `json:"session_id" jsonschema:"relay session ID to restart"`
}

type restartAgentOutput struct {
	ToolOutcome
	ChannelType string   `json:"channel_type,omitempty" jsonschema:"channel type that owns the restarted session"`
	AddressKey  string   `json:"address_key,omitempty" jsonschema:"channel-specific address key for the restarted session"`
	SessionID   string   `json:"session_id,omitempty" jsonschema:"new relay session ID"`
	TopicID     int      `json:"topic_id,omitempty" jsonschema:"Telegram topic ID when applicable"`
	ChatID      int64    `json:"chat_id,omitempty" jsonschema:"Telegram chat ID when applicable"`
	AgentName   string   `json:"agent_name,omitempty" jsonschema:"configured agent name that was restarted"`
	Description string   `json:"description,omitempty" jsonschema:"human-readable configured agent description"`
	MCPServers  []string `json:"mcp_servers,omitempty" jsonschema:"MCP server IDs mounted into the restarted session"`
}

func (s *service) restartAgent(ctx context.Context, req *mcp.CallToolRequest, in restartAgentInput) (*mcp.CallToolResult, restartAgentOutput, error) {
	operation := operationName(req, toolAgentsRestart)
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure(operation, "session_id is required")
		return result, restartAgentOutput{ToolOutcome: out}, nil
	}

	info, err := s.svc.GetSession(ctx, in.SessionID)
	if err != nil {
		result, out := backendFailure(operation, fmt.Errorf("session not found: %w", err))
		return result, restartAgentOutput{ToolOutcome: out}, nil
	}

	if err := s.svc.StopAgent(ctx, in.SessionID); err != nil {
		result, out := backendFailure(operation, fmt.Errorf("failed to stop session: %w", err))
		return result, restartAgentOutput{ToolOutcome: out}, nil
	}

	startReq := StartRequest{
		AgentName: info.AgentName,
		Locator: &StartLocator{
			ChannelType: info.ChannelType,
			Address:     map[string]any{"chat_id": info.ChatID},
		},
	}

	relayInfo, err := s.svc.StartAgent(ctx, startReq)
	if err != nil {
		result, out := backendFailure(operation, fmt.Errorf("failed to restart session: %w", err))
		return result, restartAgentOutput{ToolOutcome: out}, nil
	}

	return nil, restartAgentOutput{
		ToolOutcome: okOutcome(),
		ChannelType: relayInfo.ChannelType,
		AddressKey:  relayInfo.AddressKey,
		SessionID:   relayInfo.SessionID,
		TopicID:     relayInfo.TopicID,
		ChatID:      relayInfo.ChatID,
		AgentName:   relayInfo.AgentName,
		Description: relayInfo.Description,
		MCPServers:  relayInfo.MCPServers,
	}, nil
}

type listAgentsOutput struct {
	ToolOutcome
	Agents []AgentInfo `json:"agents,omitempty" jsonschema:"relay session objects for active and persisted sessions"`
}

func (s *service) listAgents(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listAgentsOutput, error) {
	operation := operationName(req, toolAgentsList)
	agents, err := s.svc.ListAgents(ctx)
	if err != nil {
		result, out := backendFailure(operation, err)
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

func (s *service) getAgent(ctx context.Context, req *mcp.CallToolRequest, in getAgentInput) (*mcp.CallToolResult, getAgentOutput, error) {
	operation := operationName(req, toolAgentsGet)
	if strings.TrimSpace(in.SessionID) == "" {
		result, out := validationFailure(operation, "session_id is required")
		return result, getAgentOutput{ToolOutcome: out}, nil
	}

	agent, err := s.svc.GetSession(ctx, in.SessionID)
	if err != nil {
		result, out := validationFailure(operation, fmt.Sprintf("session not found: %v", err))
		return result, getAgentOutput{ToolOutcome: out}, nil
	}

	return nil, getAgentOutput{
		ToolOutcome: okOutcome(),
		Agent:       &agent,
	}, nil
}
