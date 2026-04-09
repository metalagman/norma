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
	if out.Description != "opencode: type=opencode_acp model=opencode/big-pickle" {
		t.Fatalf("startAgent() description = %q", out.Description)
	}
	if !reflect.DeepEqual(out.MCPServers, []string{"norma.config", "norma.state"}) {
		t.Fatalf("startAgent() mcp_servers = %#v", out.MCPServers)
	}
}

type fakeRelayService struct {
	startInfo AgentInfo
	startErr  error
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
	return nil, nil
}

func (f fakeRelayService) GetSession(_ context.Context, _ string) (AgentInfo, error) {
	return AgentInfo{}, nil
}
