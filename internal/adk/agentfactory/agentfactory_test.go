package agentfactory

import (
	"context"
	"io"
	"testing"

	"github.com/metalagman/norma/internal/adk/agentconfig"
	"github.com/stretchr/testify/assert"
)

func TestFactory_CreateAgent(t *testing.T) {
	agents := map[string]agentconfig.Config{
		"test-exec": {
			Type: agentconfig.AgentTypeExec,
			Cmd:  []string{"echo", "hello"},
		},
		"test-claude": {
			Type: agentconfig.AgentTypeClaude,
		},
		"test-gemini": {
			Type: agentconfig.AgentTypeGemini,
		},
		"test-codex": {
			Type: agentconfig.AgentTypeCodex,
		},
		"test-opencode": {
			Type: agentconfig.AgentTypeOpenCode,
		},
		"test-acp": {
			Type: agentconfig.AgentTypeGeminiACP,
		},
	}
	f := NewFactory(agents)

	t.Run("Create Exec Agent", func(t *testing.T) {
		req := CreationRequest{
			Name:        "TestExec",
			Description: "Test Description",
			Stdout:      io.Discard,
			Stderr:      io.Discard,
		}
		ag, err := f.CreateAgent(context.Background(), "test-exec", req)
		assert.NoError(t, err)
		assert.NotNil(t, ag)
	})

	t.Run("Create Claude Agent", func(t *testing.T) {
		req := CreationRequest{
			Name:        "TestClaude",
			Description: "Test Description",
		}
		ag, err := f.CreateAgent(context.Background(), "test-claude", req)
		assert.NoError(t, err)
		assert.NotNil(t, ag)
	})

	t.Run("Create ACP Agent", func(t *testing.T) {
		req := CreationRequest{
			Name:        "TestACP",
			Description: "Test Description",
		}
		ag, err := f.CreateAgent(context.Background(), "test-acp", req)
		assert.NoError(t, err)
		assert.NotNil(t, ag)
	})

	t.Run("Unknown Agent", func(t *testing.T) {
		req := CreationRequest{
			Name: "Unknown",
		}
		ag, err := f.CreateAgent(context.Background(), "unknown", req)
		assert.Error(t, err)
		assert.Nil(t, ag)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestResolveCmd(t *testing.T) {
	tests := []struct {
		name    string
		cfg     agentconfig.Config
		want    []string
		wantErr bool
	}{
		{
			name: "Exec with cmd",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeExec,
				Cmd:  []string{"ls", "-la"},
			},
			want: []string{"ls", "-la"},
		},
		{
			name: "Exec without cmd",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeExec,
			},
			wantErr: true,
		},
		{
			name: "Claude default",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeClaude,
			},
			want: []string{"claude"},
		},
		{
			name: "Claude with model",
			cfg: agentconfig.Config{
				Type:  agentconfig.AgentTypeClaude,
				Model: "claude-3",
			},
			want: []string{"claude", "--model", "claude-3"},
		},
		{
			name: "Gemini with templated cmd",
			cfg: agentconfig.Config{
				Type:  agentconfig.AgentTypeGemini,
				Cmd:   []string{"gemini", "run", "--model", "{{.Model}}"},
				Model: "gemini-1.5",
			},
			want: []string{"gemini", "run", "--model", "gemini-1.5"},
		},
		{
			name: "Gemini default",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeGemini,
			},
			want: []string{"gemini", "--approval-mode", "yolo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveCmd(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestResolveACPCommand(t *testing.T) {
	tests := []struct {
		name    string
		cfg     agentconfig.Config
		want    []string
		wantErr bool
	}{
		{
			name: "ACP Exec with cmd",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeACPExec,
				Cmd:  []string{"custom-acp", "server"},
			},
			want: []string{"custom-acp", "server"},
		},
		{
			name: "Gemini ACP with model",
			cfg: agentconfig.Config{
				Type:  agentconfig.AgentTypeGeminiACP,
				Model: "gemini-pro",
			},
			want: []string{"gemini", "--experimental-acp", "--model", "gemini-pro"},
		},
		{
			name: "OpenCode ACP",
			cfg: agentconfig.Config{
				Type: agentconfig.AgentTypeOpenCodeACP,
			},
			want: []string{"opencode", "acp"},
		},
		{
			name: "Codex ACP with model and templated extra args",
			cfg: agentconfig.Config{
				Type:      agentconfig.AgentTypeCodexACP,
				Model:     "gpt-5.4",
				ExtraArgs: []string{"--codex-model={{.Model}}", "--trace"},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveACPCommand(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.cfg.Type == agentconfig.AgentTypeCodexACP {
					assert.GreaterOrEqual(t, len(got), 7)
					assert.Equal(t, "proxy", got[1])
					assert.Equal(t, "codex-acp", got[2])
					assert.Equal(t, "--codex-model", got[3])
					assert.Equal(t, "gpt-5.4", got[4])
					assert.Equal(t, "--codex-model=gpt-5.4", got[5])
					assert.Equal(t, "--trace", got[6])
					return
				}
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
