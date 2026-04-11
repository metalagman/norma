package relaymcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRelayServerPublishesInstructionsAndSchemas(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &fakeRelayService{})
	defer cleanup()

	initResult := session.InitializeResult()
	if !strings.Contains(initResult.Instructions, "manage relay agent sessions") {
		t.Fatalf("InitializeResult().Instructions = %q, want relay-agent guidance", initResult.Instructions)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	toolByName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		toolByName[tool.Name] = tool
	}

	if _, ok := toolByName["relay.agents.start"]; !ok {
		t.Fatalf("tools missing relay.agents.start: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.stop"]; !ok {
		t.Fatalf("tools missing relay.agents.stop: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.get"]; !ok {
		t.Fatalf("tools missing relay.agents.get: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.list"]; !ok {
		t.Fatalf("tools missing relay.agents.list: %#v", toolByName)
	}
	if got := toolByName["relay.agents.list"].Description; !strings.Contains(got, "persisted") {
		t.Fatalf("relay.agents.list description = %q, want persisted-session guidance", got)
	}
	if _, ok := toolByName["relay.agents.start_agent"]; ok {
		t.Fatalf("tools unexpectedly still expose relay.agents.start_agent: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.stop_agent"]; !ok {
		t.Fatalf("tools missing relay.agents.stop_agent alias: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.get_agent"]; !ok {
		t.Fatalf("tools missing relay.agents.get_agent alias: %#v", toolByName)
	}
	if _, ok := toolByName["relay.agents.list_agents"]; !ok {
		t.Fatalf("tools missing relay.agents.list_agents alias: %#v", toolByName)
	}

	outSchema, ok := toolByName["relay.agents.get"].OutputSchema.(map[string]any)
	if !ok {
		t.Fatalf("relay.agents.get output schema type = %T, want map[string]any", toolByName["relay.agents.get"].OutputSchema)
	}
	properties := outSchema["properties"].(map[string]any)
	agent := properties["agent"].(map[string]any)
	agentProperties := agent["properties"].(map[string]any)
	if _, ok := agentProperties["session_id"]; !ok {
		t.Fatalf("relay.agents.get schema missing session_id: %#v", agentProperties)
	}
	if _, ok := agentProperties["agent_name"]; !ok {
		t.Fatalf("relay.agents.get schema missing agent_name: %#v", agentProperties)
	}
	if _, ok := agentProperties["SessionID"]; ok {
		t.Fatalf("relay.agents.get schema unexpectedly contains legacy SessionID key: %#v", agentProperties)
	}
}

func TestStartAgentIncludesDescriptionAndMCPServers(t *testing.T) {
	svc := &fakeRelayService{
		startInfo: AgentInfo{
			ChannelType: "telegram",
			AddressKey:  "1:2",
			SessionID:   "tg-1-2",
			AgentName:   "opencode",
			ChatID:      1,
			TopicID:     2,
			Description: "opencode: type=opencode_acp model=opencode/big-pickle",
			MCPServers:  []string{"relay"},
		},
	}
	s := &service{
		svc: svc,
	}

	headers := http.Header{}
	headers.Set(CallerSessionIDHeader, "tg-1-0")
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: headers}}
	result, out, err := s.startAgent(context.Background(), req, startAgentInput{
		AgentName: "opencode",
	})
	if err != nil {
		t.Fatalf("startAgent() error = %v", err)
	}
	if result != nil {
		t.Fatalf("startAgent() result = %#v, want nil", result)
	}
	if !out.OK {
		t.Fatalf("startAgent() out.OK = false, want true; out=%#v", out)
	}
	if out.ChannelType != "telegram" || out.AddressKey != "1:2" {
		t.Fatalf("startAgent() channel info = (%q,%q), want (telegram,1:2)", out.ChannelType, out.AddressKey)
	}
	if out.Description != "opencode: type=opencode_acp model=opencode/big-pickle" {
		t.Fatalf("startAgent() description = %q", out.Description)
	}
	if !reflect.DeepEqual(out.MCPServers, []string{"relay"}) {
		t.Fatalf("startAgent() mcp_servers = %#v", out.MCPServers)
	}
	if got := svc.startReq.CallerSessionID; got != "tg-1-0" {
		t.Fatalf("StartAgent caller_session_id = %q, want tg-1-0", got)
	}
}

func TestStartAgentRequiresLocatorOrCallerContext(t *testing.T) {
	s := &service{
		svc: &fakeRelayService{},
	}

	result, out, err := s.startAgent(context.Background(), nil, startAgentInput{
		AgentName: "alpha",
	})
	if err != nil {
		t.Fatalf("startAgent() error = %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("startAgent() result = %#v, want validation error", result)
	}
	if out.OK {
		t.Fatalf("startAgent() output = %#v, want validation failure", out)
	}
}

func TestStartAgentAcceptsExplicitLocator(t *testing.T) {
	svc := &fakeRelayService{
		startInfo: AgentInfo{
			ChannelType: "telegram",
			AddressKey:  "1:7",
			SessionID:   "tg-1-7",
			AgentName:   "alpha",
			ChatID:      1,
			TopicID:     7,
		},
	}
	s := &service{svc: svc}

	result, out, err := s.startAgent(context.Background(), nil, startAgentInput{
		AgentName: "alpha",
		Locator: &StartLocator{
			ChannelType: "telegram",
			Address: map[string]any{
				"chat_id": float64(1),
			},
		},
	})
	if err != nil {
		t.Fatalf("startAgent() error = %v", err)
	}
	if result != nil {
		t.Fatalf("startAgent() result = %#v, want nil", result)
	}
	if !out.OK || out.ChatID != 1 || out.TopicID != 7 {
		t.Fatalf("startAgent() output = %#v, want started agent info", out)
	}
}

func TestListAgentsReturnsStructuredAgents(t *testing.T) {
	want := []AgentInfo{{
		ChannelType: "telegram",
		AddressKey:  "9:3",
		SessionID:   "tg-9-3",
		AgentName:   "opencode",
		ChatID:      9,
		TopicID:     3,
		Status:      "persisted",
	}}
	s := &service{svc: &fakeRelayService{listInfo: want}}

	result, out, err := s.listAgents(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("listAgents() error = %v", err)
	}
	if result != nil {
		t.Fatalf("listAgents() result = %#v, want nil", result)
	}
	if !reflect.DeepEqual(out.Agents, want) {
		t.Fatalf("listAgents() agents = %#v, want %#v", out.Agents, want)
	}
}

func TestGetAgentReturnsStructuredAgent(t *testing.T) {
	want := AgentInfo{
		ChannelType: "telegram",
		AddressKey:  "9:0",
		SessionID:   "tg-9-0",
		AgentName:   "root",
		ChatID:      9,
		TopicID:     0,
		Status:      "active",
	}
	s := &service{svc: &fakeRelayService{sessionInfo: want}}

	result, out, err := s.getAgent(context.Background(), nil, getAgentInput{SessionID: "tg-9-0"})
	if err != nil {
		t.Fatalf("getAgent() error = %v", err)
	}
	if result != nil {
		t.Fatalf("getAgent() result = %#v, want nil", result)
	}
	if out.Agent == nil || !reflect.DeepEqual(*out.Agent, want) {
		t.Fatalf("getAgent() agent = %#v, want %#v", out.Agent, want)
	}
}

func TestRelayAgentStructuredOutputUsesSnakeCase(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &fakeRelayService{sessionInfo: AgentInfo{
		ChannelType: "telegram",
		AddressKey:  "9:0",
		SessionID:   "tg-9-0",
		AgentName:   "root",
		ChatID:      9,
		TopicID:     0,
		Status:      "active",
	}})
	defer cleanup()
	_ = session.InitializeResult()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "relay.agents.get",
		Arguments: map[string]any{"session_id": "tg-9-0"},
	})
	if err != nil {
		t.Fatalf("CallTool(get) error = %v", err)
	}
	payload := structuredResultMap(t, result)
	agent := payload["agent"].(map[string]any)
	if agent["session_id"] != "tg-9-0" {
		t.Fatalf("agent.session_id = %v, want tg-9-0", agent["session_id"])
	}
	if agent["agent_name"] != "root" {
		t.Fatalf("agent.agent_name = %v, want root", agent["agent_name"])
	}
	if _, ok := agent["SessionID"]; ok {
		t.Fatalf("agent unexpectedly contains legacy SessionID field: %#v", agent)
	}
}

func TestLifecycleToolValidationFailureUsesInvokedOperationName(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &fakeRelayService{
		stopErr: errors.New("stop failed"),
		getErr:  errors.New("session not found"),
	})
	defer cleanup()
	_ = session.InitializeResult()

	tests := []struct {
		name string
		tool string
	}{
		{name: "canonical stop", tool: "relay.agents.stop"},
		{name: "alias stop", tool: "relay.agents.stop_agent"},
		{name: "canonical get", tool: "relay.agents.get"},
		{name: "alias get", tool: "relay.agents.get_agent"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.tool,
				Arguments: map[string]any{"session_id": "tg-9-0"},
			})
			if err != nil {
				t.Fatalf("CallTool(%s) error = %v", tc.tool, err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("CallTool(%s) result = %#v, want error", tc.tool, result)
			}
			payload := structuredResultMap(t, result)
			errorObj, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("CallTool(%s) payload.error type = %T, want map[string]any", tc.tool, payload["error"])
			}
			if got := errorObj["operation"]; got != tc.tool {
				t.Fatalf("CallTool(%s) error.operation = %v, want %q", tc.tool, got, tc.tool)
			}
		})
	}
}

type fakeRelayService struct {
	startInfo   AgentInfo
	startErr    error
	startReq    StartRequest
	stopErr     error
	getErr      error
	sessionInfo AgentInfo
	listInfo    []AgentInfo
}

func (f *fakeRelayService) StartAgent(_ context.Context, req StartRequest) (AgentInfo, error) {
	if f.startErr != nil {
		return AgentInfo{}, f.startErr
	}
	f.startReq = req
	return f.startInfo, nil
}

func (f *fakeRelayService) StopAgent(_ context.Context, _ string) error {
	return f.stopErr
}

func (f *fakeRelayService) ListAgents(_ context.Context) ([]AgentInfo, error) {
	return f.listInfo, nil
}

func (f *fakeRelayService) GetSession(_ context.Context, _ string) (AgentInfo, error) {
	if f.getErr != nil {
		return AgentInfo{}, f.getErr
	}
	return f.sessionInfo, nil
}

func newTestSession(t *testing.T, svc RelayService) (context.Context, func(), *mcp.ClientSession) {
	t.Helper()

	server, err := NewServer(svc)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect() error = %v", err)
	}

	cleanup := func() {
		cancel()
		_ = session.Close()
	}
	return ctx, cleanup, session
}

func structuredResultMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	switch typed := result.StructuredContent.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			t.Fatalf("json.Unmarshal(structured content) error = %v", err)
		}
		return decoded
	case nil:
		t.Fatalf("result.StructuredContent is nil")
	default:
		t.Fatalf("unexpected structured content type %T", result.StructuredContent)
	}
	return nil
}
