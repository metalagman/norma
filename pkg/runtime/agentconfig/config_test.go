package agentconfig

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "valid_generic_acp",
			cfg: Config{
				Type: AgentTypeGenericACP,
				GenericACP: &ACPConfig{
					Cmd: []string{"custom-acp", "--stdio"},
				},
			},
		},
		{
			name: "missing_type",
			cfg: Config{
				GenericACP: &ACPConfig{Cmd: []string{"ainvoke"}},
			},
			wantErr: "type is required",
		},
		{
			name: "invalid_type",
			cfg: Config{
				Type:       "invalid",
				GenericACP: &ACPConfig{Cmd: []string{"ainvoke"}},
			},
			wantErr: "type must be one of:",
		},
		{
			name: "generic_acp_requires_cmd",
			cfg: Config{
				Type:       AgentTypeGenericACP,
				GenericACP: &ACPConfig{},
			},
			wantErr: "cmd is required for type generic_acp",
		},
		{
			name: "alias_forbids_cmd",
			cfg: Config{
				Type:      AgentTypeGeminiACP,
				GeminiACP: &ACPConfig{Cmd: []string{"gemini", "--acp"}},
			},
			wantErr: "cmd must be omitted for type gemini_acp",
		},
		{
			name: "claude_code_alias_forbids_cmd",
			cfg: Config{
				Type:          AgentTypeClaudeCodeACP,
				ClaudeCodeACP: &ACPConfig{Cmd: []string{"npx", "-y", "@zed-industries/claude-code-acp@latest"}},
			},
			wantErr: "cmd must be omitted for type claude_code_acp",
		},
		{
			name: "cmd_item_must_be_nonempty",
			cfg: Config{
				Type: AgentTypeGenericACP,
				GenericACP: &ACPConfig{
					Cmd: []string{"custom-acp", ""},
				},
			},
			wantErr: "cmd[1] must have at least 1 character",
		},
		{
			name: "extra_args_item_must_be_nonempty",
			cfg: Config{
				Type: AgentTypeGenericACP,
				GenericACP: &ACPConfig{
					Cmd:       []string{"custom-acp"},
					ExtraArgs: []string{"--trace", ""},
				},
			},
			wantErr: "extra_args[1] must have at least 1 character",
		},
		{
			name: "valid_openai",
			cfg: Config{
				Type: AgentTypeOpenAI,
				OpenAI: &LocalAPIConfig{
					APIKey: "test-key",
					Model:  "gpt-5",
				},
			},
		},
		{
			name: "openai_rejects_mcp_servers",
			cfg: Config{
				Type:       AgentTypeOpenAI,
				MCPServers: []string{"workspace"},
				OpenAI: &LocalAPIConfig{
					Model: "gpt-5",
				},
			},
			wantErr: "mcp_servers is not supported for type openai",
		},
		{
			name: "valid_aistudio",
			cfg: Config{
				Type: AgentTypeAIStudio,
				AIStudio: &LocalAPIConfig{
					Model: "gemini-2.5-flash",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() returned nil error, want substring %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNormalizeConfig(t *testing.T) {
	t.Parallel()

	const execPath = "/tmp/norma"

	tests := []struct {
		name    string
		cfg     Config
		exec    string
		want    ResolvedConfig
		wantErr string
	}{
		{
			name: "gemini_alias",
			cfg: Config{
				Type: AgentTypeGeminiACP,
				GeminiACP: &ACPConfig{
					Model:     "gemini-3-flash-preview",
					Mode:      "code",
					ExtraArgs: []string{"--trace"},
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"gemini", "--acp", "--model", "gemini-3-flash-preview", "--trace"},
				Model:   "gemini-3-flash-preview",
				Mode:    "code",
			},
		},
		{
			name: "opencode_alias",
			cfg: Config{
				Type:        AgentTypeOpenCodeACP,
				OpenCodeACP: &ACPConfig{ExtraArgs: []string{"--trace"}},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"opencode", "acp", "--trace"},
			},
		},
		{
			name: "codex_alias",
			cfg: Config{
				Type: AgentTypeCodexACP,
				CodexACP: &ACPConfig{
					Model:     "gpt-5-codex",
					Mode:      "code",
					ExtraArgs: []string{"--trace"},
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"npx", "-y", "@normahq/codex-acp-bridge@latest", "--trace"},
				Model:   "gpt-5-codex",
				Mode:    "code",
			},
		},
		{
			name: "copilot_alias",
			cfg: Config{
				Type:       AgentTypeCopilotACP,
				CopilotACP: &ACPConfig{Model: "gpt-5-codex", ExtraArgs: []string{"--trace"}},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"copilot", "--acp", "--trace"},
				Model:   "gpt-5-codex",
			},
		},
		{
			name: "claude_code_alias",
			cfg: Config{
				Type: AgentTypeClaudeCodeACP,
				ClaudeCodeACP: &ACPConfig{
					Model:     "claude-sonnet-4-20250514",
					Mode:      "code",
					ExtraArgs: []string{"--trace"},
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"npx", "-y", "@zed-industries/claude-code-acp@latest", "--trace"},
				Model:   "claude-sonnet-4-20250514",
				Mode:    "code",
			},
		},
		{
			name: "generic_with_template",
			cfg: Config{
				Type: AgentTypeGenericACP,
				GenericACP: &ACPConfig{
					Cmd:       []string{"custom-acp", "--model", "{{.Model}}"},
					Model:     "gpt-5.4",
					ExtraArgs: []string{"--trace", "--model={{.Model}}"},
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:    AgentTypeGenericACP,
				Command: []string{"custom-acp", "--model", "gpt-5.4", "--trace", "--model=gpt-5.4"},
				Model:   "gpt-5.4",
			},
		},
		{
			name: "pool",
			cfg: Config{
				Type:       AgentTypePool,
				PoolConfig: &PoolConfig{Members: []string{"a", "b"}},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:        AgentTypePool,
				PoolMembers: []string{"a", "b"},
			},
		},
		{
			name: "openai",
			cfg: Config{
				Type: AgentTypeOpenAI,
				OpenAI: &LocalAPIConfig{
					APIKey: "test-key",
					Model:  "gpt-5",
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:   AgentTypeOpenAI,
				APIKey: "test-key",
				Model:  "gpt-5",
			},
		},
		{
			name: "aistudio",
			cfg: Config{
				Type: AgentTypeAIStudio,
				AIStudio: &LocalAPIConfig{
					APIKey: "test-key",
					Model:  "gemini-2.5-flash",
				},
			},
			exec: execPath,
			want: ResolvedConfig{
				Type:   AgentTypeAIStudio,
				APIKey: "test-key",
				Model:  "gemini-2.5-flash",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeConfig(tt.cfg, tt.exec)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("NormalizeConfig returned nil error, want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("NormalizeConfig error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeConfig returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeConfig = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNormalizeConfig_DoesNotMutateSchemaConfig(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Type: AgentTypeGenericACP,
		GenericACP: &ACPConfig{
			Cmd:       []string{"custom-acp", "--model", "{{.Model}}"},
			Model:     "gpt-5.4",
			ExtraArgs: []string{"--trace"},
		},
	}
	before := cfg

	_, err := NormalizeConfig(cfg, "/tmp/norma")
	if err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatalf("NormalizeConfig mutated input cfg: got %#v, want %#v", cfg, before)
	}
}

func TestNormalizeConfigs(t *testing.T) {
	t.Parallel()

	const execPath = "/tmp/norma"

	got, err := NormalizeConfigs(map[string]Config{
		"plan": {
			Type:      AgentTypeGeminiACP,
			GeminiACP: &ACPConfig{Model: "gemini-3-flash-preview"},
		},
		"do": {
			Type:        AgentTypeOpenCodeACP,
			OpenCodeACP: &ACPConfig{},
		},
		"check": {
			Type:     AgentTypeCodexACP,
			CodexACP: &ACPConfig{Model: "gpt-5-codex"},
		},
		"act": {
			Type:          AgentTypeClaudeCodeACP,
			ClaudeCodeACP: &ACPConfig{},
		},
		"planner": {
			Type:       AgentTypeGenericACP,
			GenericACP: &ACPConfig{Cmd: []string{"custom-acp"}},
		},
	}, execPath)
	if err != nil {
		t.Fatalf("NormalizeConfigs returned error: %v", err)
	}

	planCfg := got["plan"]
	if planCfg.Type != AgentTypeGenericACP {
		t.Fatalf("plan type = %q, want %q", planCfg.Type, AgentTypeGenericACP)
	}
	if len(planCfg.Command) == 0 || planCfg.Command[0] != "gemini" {
		t.Fatalf("plan command = %v, want gemini ACP command", planCfg.Command)
	}

	doCfg := got["do"]
	if doCfg.Type != AgentTypeGenericACP {
		t.Fatalf("do type = %q, want %q", doCfg.Type, AgentTypeGenericACP)
	}
	if len(doCfg.Command) < 2 || doCfg.Command[0] != "opencode" || doCfg.Command[1] != "acp" {
		t.Fatalf("do command = %v, want opencode acp", doCfg.Command)
	}

	checkCfg := got["check"]
	if checkCfg.Type != AgentTypeGenericACP {
		t.Fatalf("check type = %q, want %q", checkCfg.Type, AgentTypeGenericACP)
	}
	if len(checkCfg.Command) < 3 || checkCfg.Command[0] != "npx" || checkCfg.Command[1] != "-y" || checkCfg.Command[2] != "@normahq/codex-acp-bridge@latest" {
		t.Fatalf("check command = %v, want npx -y @normahq/codex-acp-bridge@latest", checkCfg.Command)
	}

	actCfg := got["act"]
	if actCfg.Type != AgentTypeGenericACP {
		t.Fatalf("act type = %q, want %q", actCfg.Type, AgentTypeGenericACP)
	}
	if len(actCfg.Command) < 3 || actCfg.Command[0] != "npx" || actCfg.Command[1] != "-y" || actCfg.Command[2] != "@zed-industries/claude-code-acp@latest" {
		t.Fatalf("act command = %v, want npx -y @zed-industries/claude-code-acp@latest", actCfg.Command)
	}
}
