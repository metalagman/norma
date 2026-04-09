package relaymcp

import (
	"context"
	"reflect"
	"testing"
)

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
				MCPServers:  []string{"norma.config", "norma.state"},
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
	if !reflect.DeepEqual(out.MCPServers, []string{"norma.config", "norma.state"}) {
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
