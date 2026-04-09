package relaymcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRelayServerPublishesInstructionsAndSchemas(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, fakeRelayService{})
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

	if got := toolByName["relay.agents.list_agents"].Description; !strings.Contains(got, "persisted") {
		t.Fatalf("relay.agents.list_agents description = %q, want persisted-session guidance", got)
	}

	outSchema, ok := toolByName["relay.agents.get_agent"].OutputSchema.(map[string]any)
	if !ok {
		t.Fatalf("relay.agents.get_agent output schema type = %T, want map[string]any", toolByName["relay.agents.get_agent"].OutputSchema)
	}
	properties := outSchema["properties"].(map[string]any)
	agent := properties["agent"].(map[string]any)
	agentProperties := agent["properties"].(map[string]any)
	if _, ok := agentProperties["session_id"]; !ok {
		t.Fatalf("relay.agents.get_agent schema missing session_id: %#v", agentProperties)
	}
	if _, ok := agentProperties["agent_name"]; !ok {
		t.Fatalf("relay.agents.get_agent schema missing agent_name: %#v", agentProperties)
	}
	if _, ok := agentProperties["SessionID"]; ok {
		t.Fatalf("relay.agents.get_agent schema unexpectedly contains legacy SessionID key: %#v", agentProperties)
	}
}

func TestStartAgentIncludesDescriptionAndMCPServers(t *testing.T) {
	s := &service{
		svc: fakeRelayService{
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
		},
	}

	result, out, err := s.startAgent(context.Background(), nil, startAgentInput{
		ChatID:    1,
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
}

func TestStartAgentCanInferChatFromSessionID(t *testing.T) {
	s := &service{
		svc: fakeRelayService{
			startInfo: AgentInfo{
				ChannelType: "telegram",
				AddressKey:  "1:7",
				SessionID:   "tg-1-7",
				AgentName:   "alpha",
				ChatID:      1,
				TopicID:     7,
			},
			sessionInfo: AgentInfo{
				SessionID:   "tg-1-0",
				ChannelType: "telegram",
				AddressKey:  "1:0",
				ChatID:      1,
				TopicID:     0,
			},
		},
	}

	result, out, err := s.startAgent(context.Background(), nil, startAgentInput{
		SessionID: "tg-1-0",
		AgentName: "alpha",
	})
	if err != nil {
		t.Fatalf("startAgent() error = %v", err)
	}
	if result != nil {
		t.Fatalf("startAgent() result = %#v, want nil", result)
	}
	if !out.OK || out.ChatID != 1 || out.TopicID != 7 {
		t.Fatalf("startAgent() output = %#v, want inferred chat context", out)
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
	s := &service{svc: fakeRelayService{listInfo: want}}

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
	s := &service{svc: fakeRelayService{sessionInfo: want}}

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
	ctx, cleanup, session := newTestSession(t, fakeRelayService{sessionInfo: AgentInfo{
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
		Name:      "relay.agents.get_agent",
		Arguments: map[string]any{"session_id": "tg-9-0"},
	})
	if err != nil {
		t.Fatalf("CallTool(get_agent) error = %v", err)
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

type fakeRelayService struct {
	startInfo   AgentInfo
	startErr    error
	sessionInfo AgentInfo
	listInfo    []AgentInfo
}

func (f fakeRelayService) StartAgent(_ context.Context, _ int64, _ string) (AgentInfo, error) {
	if f.startErr != nil {
		return AgentInfo{}, f.startErr
	}
	return f.startInfo, nil
}

func (f fakeRelayService) StopAgent(_ context.Context, _ string) error {
	return nil
}

func (f fakeRelayService) ListAgents(_ context.Context) ([]AgentInfo, error) {
	return f.listInfo, nil
}

func (f fakeRelayService) GetSession(_ context.Context, _ string) (AgentInfo, error) {
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
