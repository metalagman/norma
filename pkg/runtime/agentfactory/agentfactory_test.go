package agentfactory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/pkg/runtime/acpagent"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/hostedagent"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/normahq/norma/pkg/runtime/sessionstate"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
)

type contextKey string

func TestFactory_CreateAgent(t *testing.T) {
	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: helperACPCommand(t),
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	t.Run("Create ACP Agent", func(t *testing.T) {
		req := BuildRequest{
			AgentID:          "test-acp",
			Name:             "TestACP",
			Description:      "Test Description",
			WorkingDirectory: t.TempDir(),
		}
		ag, err := f.Build(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, ag)
	})

	t.Run("Unknown Agent", func(t *testing.T) {
		req := BuildRequest{
			AgentID:          "unknown",
			Name:             "Unknown",
			WorkingDirectory: t.TempDir(),
		}
		ag, err := f.Build(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, ag)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Missing working directory", func(t *testing.T) {
		req := BuildRequest{
			AgentID: "test-acp",
			Name:    "TestACP",
		}
		ag, err := f.Build(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, ag)
		assert.Contains(t, err.Error(), "working_directory is required")
	})
}

func helperACPCommand(t *testing.T) []string {
	t.Helper()
	return []string{
		"env",
		"GO_WANT_AGENTFACTORY_ACP_HELPER=1",
		os.Args[0],
		"-test.run=TestAgentFactoryACPHelperProcess",
		"--",
	}
}

func TestAgentFactoryACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AGENTFACTORY_ACP_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error": map[string]any{
					"code":    -32700,
					"message": "parse error",
				},
			})
			continue
		}

		if req.Method == acp.AgentMethodInitialize {
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": acp.ProtocolVersionNumber,
				},
			})
			continue
		}

		_ = encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "unsupported",
			},
		})
	}
	os.Exit(0)
}

func TestFactoryBuild_NormalizesTemplatedACPCommand(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedCommand []string
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedCommand = append([]string(nil), cfg.Command...)
		return nil, nil
	}

	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd:       []string{"custom-acp", "--model", "{{.Model}}"},
				Model:     "gpt-5.4",
				ExtraArgs: []string{"--trace", "--model={{.Model}}"},
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{AgentID: "test-acp", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"custom-acp", "--model", "gpt-5.4", "--trace", "--model=gpt-5.4"}
	assert.Equal(t, want, capturedCommand)
}

func TestFactoryBuild_PropagatesCodexReasoningEffort(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedReasoningEffort string
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedReasoningEffort = cfg.ReasoningEffort
		return nil, nil
	}

	agents := map[string]agentconfig.Config{
		"codex": {
			Type:            agentconfig.AgentTypeCodexACP,
			ReasoningEffort: "medium",
			CodexACP: &agentconfig.ACPConfig{
				Model: "gpt-5-codex",
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{AgentID: "codex", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	assert.Equal(t, "medium", capturedReasoningEffort)
}

func TestFactoryBuild_BuildRequestReasoningEffortOverridesProvider(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedReasoningEffort string
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedReasoningEffort = cfg.ReasoningEffort
		return nil, nil
	}

	agents := map[string]agentconfig.Config{
		"codex": {
			Type:            agentconfig.AgentTypeCodexACP,
			ReasoningEffort: "low",
			CodexACP: &agentconfig.ACPConfig{
				Model: "gpt-5-codex",
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:          "codex",
		WorkingDirectory: t.TempDir(),
		ReasoningEffort:  "high",
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	assert.Equal(t, "high", capturedReasoningEffort)
}

func TestFactoryBuild_RejectsReasoningEffortOverrideForUnsupportedType(t *testing.T) {
	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: helperACPCommand(t),
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:          "test-acp",
		WorkingDirectory: t.TempDir(),
		ReasoningEffort:  "high",
	})
	if err == nil {
		t.Fatal("Build() error = nil, want unsupported reasoning_effort error")
	}
	assert.Contains(t, err.Error(), "reasoning_effort is only supported for type codex_acp")
}

func TestACPConstructor_PropagatesContextLogger(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedLogger *zerolog.Logger
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedLogger = cfg.Logger
		return nil, nil
	}

	var logBuf bytes.Buffer
	baseLogger := zerolog.New(&logBuf).Level(zerolog.TraceLevel)
	ctx := baseLogger.WithContext(context.Background())
	factory := New(map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}, nil)

	_, err := acpConstructor(ctx, agentconfig.ResolvedConfig{
		Type:    agentconfig.AgentTypeGenericACP,
		Command: []string{"fake-acp", "serve"},
	}, BuildRequest{
		AgentID:          "test-acp",
		Name:             "test-acp",
		Description:      "test",
		WorkingDirectory: t.TempDir(),
	}, factory, nil)
	if err != nil {
		t.Fatalf("acpConstructor() error = %v", err)
	}
	if capturedLogger == nil {
		t.Fatal("acpConstructor() did not pass logger to acpagent config")
	}
	if capturedLogger.GetLevel() != zerolog.TraceLevel {
		t.Fatalf("captured logger level = %s, want %s", capturedLogger.GetLevel(), zerolog.TraceLevel)
	}
}

func TestFactoryBuild_DefaultACPStderrWriterIsProcessStderr(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedStderr any
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedStderr = cfg.Stderr
		return nil, nil
	}

	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}
	f := New(agents, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:          "test-acp",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	stderrFile, ok := capturedStderr.(*os.File)
	if !ok {
		t.Fatalf("ACP stderr writer type = %T, want *os.File", capturedStderr)
	}
	if stderrFile != os.Stderr {
		t.Fatalf("ACP stderr writer = %p, want process stderr %p", stderrFile, os.Stderr)
	}
}

func TestFactoryBuild_WithStderrWriterOverridesACPStderr(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedStderr any
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedStderr = cfg.Stderr
		return nil, nil
	}

	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}
	customStderr := &bytes.Buffer{}
	f := New(agents, mcpregistry.New(nil), WithStderrWriter(customStderr))

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:          "test-acp",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if capturedStderr != customStderr {
		t.Fatalf("ACP stderr writer = %T/%p, want custom writer %p", capturedStderr, capturedStderr, customStderr)
	}
}

func TestFactoryBuild_UsesBuildRequestMCPServerIDsOverride(t *testing.T) {
	origNewACPAgent := newACPAgent
	origProcessEnv := processEnv
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
		processEnv = origProcessEnv
	})

	var capturedMCP map[string]acpagent.MCPServerConfig
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedMCP = cfg.MCPServers
		return nil, nil
	}
	processEnv = func() []string {
		return []string{"BASE_TOKEN=from-process", "UNCHANGED=process-value"}
	}

	agents := map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
			MCPServers: []string{"cfg"},
		},
	}
	reg := mcpregistry.New(map[string]agentconfig.MCPServerConfig{
		"cfg": {
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  "http://cfg.example/mcp",
		},
		"override": {
			Type: agentconfig.MCPServerTypeStdio,
			Cmd:  []string{"override-server"},
			Env: map[string]string{
				"BASE_TOKEN": "from-config",
				"LOCAL_ONLY": "config-value",
				"EMPTY":      "",
			},
		},
	})
	f := New(agents, reg)

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:          "test-acp",
		WorkingDirectory: t.TempDir(),
		MCPServerIDs:     []string{"override"},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(capturedMCP) != 1 {
		t.Fatalf("len(capturedMCP) = %d, want 1", len(capturedMCP))
	}
	if _, ok := capturedMCP["override"]; !ok {
		t.Fatalf("captured MCP does not contain override server: %#v", capturedMCP)
	}
	if _, ok := capturedMCP["cfg"]; ok {
		t.Fatalf("captured MCP unexpectedly contains cfg server: %#v", capturedMCP)
	}
	override := capturedMCP["override"]
	if got := override.Env["BASE_TOKEN"]; got != "from-config" {
		t.Fatalf("override BASE_TOKEN = %q, want from-config", got)
	}
	if got := override.Env["LOCAL_ONLY"]; got != "config-value" {
		t.Fatalf("override LOCAL_ONLY = %q, want config-value", got)
	}
	if got := override.Env["UNCHANGED"]; got != "process-value" {
		t.Fatalf("override UNCHANGED = %q, want process-value", got)
	}
	if got := override.Env["EMPTY"]; got != "" {
		t.Fatalf("override EMPTY = %q, want empty string", got)
	}

	original, ok := reg.Get("override")
	if !ok {
		t.Fatal("registry missing override MCP server")
	}
	if got := original.Env["UNCHANGED"]; got != "" {
		t.Fatalf("registry env mutated, UNCHANGED = %q, want empty string", got)
	}
}

func TestFactoryBuildSessionState_UsesCanonicalCWDKey(t *testing.T) {
	workingDir := t.TempDir()
	f := New(map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}, nil)

	state, err := f.BuildSessionState("test-acp", workingDir)
	if err != nil {
		t.Fatalf("BuildSessionState() error = %v", err)
	}
	if len(state) != 1 {
		t.Fatalf("len(state) = %d, want 1", len(state))
	}
	gotCWD, ok := state[sessionstate.CWDKey].(string)
	if !ok {
		t.Fatalf("state[%q] type = %T, want string", sessionstate.CWDKey, state[sessionstate.CWDKey])
	}
	wantCWD, err := filepath.Abs(workingDir)
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	if gotCWD != wantCWD {
		t.Fatalf("state[%q] = %q, want %q", sessionstate.CWDKey, gotCWD, wantCWD)
	}
}

func TestFactoryBuildSessionState_InvalidCWD(t *testing.T) {
	f := New(map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}, nil)

	_, err := f.BuildSessionState("test-acp", filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("BuildSessionState() error = nil, want invalid cwd error")
	}
	if !strings.Contains(err.Error(), "stat session cwd") {
		t.Fatalf("BuildSessionState() error = %q, want stat session cwd", err)
	}
}

func TestFactoryBuildSessionState_UnknownAgent(t *testing.T) {
	f := New(map[string]agentconfig.Config{}, nil)
	_, err := f.BuildSessionState("unknown", t.TempDir())
	if err == nil {
		t.Fatal("BuildSessionState() error = nil, want unknown agent error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("BuildSessionState() error = %q, want not found", err)
	}
}

func TestACPConstructor_UsesInstructionAndGlobalInstruction(t *testing.T) {
	origNewACPAgent := newACPAgent
	t.Cleanup(func() {
		newACPAgent = origNewACPAgent
	})

	var capturedInstruction string
	var capturedGlobalInstruction string
	var capturedOutputKey string
	newACPAgent = func(cfg acpagent.Config) (agent.Agent, error) {
		capturedInstruction = cfg.Instruction
		capturedGlobalInstruction = cfg.GlobalInstruction
		capturedOutputKey = cfg.OutputKey
		return nil, nil
	}
	factory := New(map[string]agentconfig.Config{
		"test-acp": {
			Type: agentconfig.AgentTypeGenericACP,
			GenericACP: &agentconfig.ACPConfig{
				Cmd: []string{"fake-acp", "serve"},
			},
		},
	}, nil)

	_, err := acpConstructor(context.Background(), agentconfig.ResolvedConfig{
		Type:               agentconfig.AgentTypeGenericACP,
		Command:            []string{"fake-acp", "serve"},
		SystemInstructions: "from-config",
	}, BuildRequest{
		AgentID:           "test-acp",
		Instruction:       "from-request",
		GlobalInstruction: "global-request",
		OutputKey:         "agent_result",
		WorkingDirectory:  t.TempDir(),
	}, factory, nil)
	if err != nil {
		t.Fatalf("acpConstructor() error = %v", err)
	}

	if capturedInstruction != "from-request" {
		t.Fatalf("cfg.Instruction = %q, want from-request", capturedInstruction)
	}
	if capturedGlobalInstruction != "global-request" {
		t.Fatalf("cfg.GlobalInstruction = %q, want global-request", capturedGlobalInstruction)
	}
	if capturedOutputKey != "agent_result" {
		t.Fatalf("cfg.OutputKey = %q, want agent_result", capturedOutputKey)
	}
}

type fakeHostedModel struct {
	name string
}

func (m fakeHostedModel) Name() string {
	return m.name
}

func (m fakeHostedModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func TestFactoryBuild_OpenAIProvider(t *testing.T) {
	origNewOpenAIModel := newOpenAIModel
	origNewHostedAgent := newHostedAgent
	t.Cleanup(func() {
		newOpenAIModel = origNewOpenAIModel
		newHostedAgent = origNewHostedAgent
	})

	var capturedAPIKey string
	var capturedModelName string
	newOpenAIModel = func(apiKey, modelName string) (model.LLM, error) {
		capturedAPIKey = apiKey
		capturedModelName = modelName
		return fakeHostedModel{name: "remote-openai"}, nil
	}

	var capturedCfg hostedagent.Config
	newHostedAgent = func(cfg hostedagent.Config) (agent.Agent, error) {
		capturedCfg = cfg
		return nil, nil
	}

	f := New(map[string]agentconfig.Config{
		"openai": {
			Type: agentconfig.AgentTypeOpenAI,
			OpenAI: &agentconfig.LocalAPIConfig{
				APIKey: "openai-test-key",
				Model:  "gpt-5",
			},
			SystemInstructions: "from-config",
		},
	}, mcpregistry.New(nil))

	_, err := f.Build(context.Background(), BuildRequest{
		AgentID:           "openai",
		Name:              "shell-openai",
		Description:       "OpenAI shell agent",
		Instruction:       "from-request",
		GlobalInstruction: "global-request",
		WorkingDirectory:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if capturedAPIKey != "openai-test-key" {
		t.Fatalf("openai api key = %q, want openai-test-key", capturedAPIKey)
	}
	if capturedModelName != "gpt-5" {
		t.Fatalf("openai model name = %q, want gpt-5", capturedModelName)
	}
	if capturedCfg.Name != "shell-openai" {
		t.Fatalf("hosted agent name = %q, want shell-openai", capturedCfg.Name)
	}
	if capturedCfg.Description != "OpenAI shell agent" {
		t.Fatalf("hosted agent description = %q, want OpenAI shell agent", capturedCfg.Description)
	}
	if capturedCfg.Instruction != "from-request" {
		t.Fatalf("hosted agent instruction = %q, want from-request", capturedCfg.Instruction)
	}
	if capturedCfg.GlobalInstruction != "global-request" {
		t.Fatalf("hosted agent global instruction = %q, want global-request", capturedCfg.GlobalInstruction)
	}
	if capturedCfg.Model == nil || capturedCfg.Model.Name() != "remote-openai" {
		t.Fatalf("hosted agent model = %#v, want remote-openai", capturedCfg.Model)
	}
}

func TestFactoryBuild_AIStudioProvider(t *testing.T) {
	origNewAIStudioModel := newAIStudioModel
	origNewHostedAgent := newHostedAgent
	t.Cleanup(func() {
		newAIStudioModel = origNewAIStudioModel
		newHostedAgent = origNewHostedAgent
	})

	var capturedCtx context.Context
	var capturedAPIKey string
	var capturedModelName string
	newAIStudioModel = func(ctx context.Context, apiKey, modelName string) (model.LLM, error) {
		capturedCtx = ctx
		capturedAPIKey = apiKey
		capturedModelName = modelName
		return fakeHostedModel{name: "remote-aistudio"}, nil
	}

	var capturedCfg hostedagent.Config
	newHostedAgent = func(cfg hostedagent.Config) (agent.Agent, error) {
		capturedCfg = cfg
		return nil, nil
	}

	ctx := context.WithValue(context.Background(), contextKey("provider"), "aistudio")
	f := New(map[string]agentconfig.Config{
		"aistudio": {
			Type: agentconfig.AgentTypeAIStudio,
			AIStudio: &agentconfig.LocalAPIConfig{
				APIKey: "aistudio-test-key",
				Model:  "gemini-2.5-flash",
			},
		},
	}, mcpregistry.New(nil))

	_, err := f.Build(ctx, BuildRequest{
		AgentID:          "aistudio",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if capturedCtx != ctx {
		t.Fatal("aistudio constructor did not pass the request context through to model creation")
	}
	if capturedAPIKey != "aistudio-test-key" {
		t.Fatalf("aistudio api key = %q, want aistudio-test-key", capturedAPIKey)
	}
	if capturedModelName != "gemini-2.5-flash" {
		t.Fatalf("aistudio model name = %q, want gemini-2.5-flash", capturedModelName)
	}
	if capturedCfg.Name != "aistudio" {
		t.Fatalf("hosted agent name = %q, want aistudio", capturedCfg.Name)
	}
	if capturedCfg.Model == nil || capturedCfg.Model.Name() != "remote-aistudio" {
		t.Fatalf("hosted agent model = %#v, want remote-aistudio", capturedCfg.Model)
	}
}

func TestValidatePoolMembers_AcceptsMixedACPAndHostedProviders(t *testing.T) {
	members, err := validatePoolMembers("pool", []string{"codex", "openai"}, map[string]agentconfig.Config{
		"codex": {
			Type: agentconfig.AgentTypeCodexACP,
			CodexACP: &agentconfig.ACPConfig{
				Model: "gpt-5-codex",
			},
		},
		"openai": {
			Type: agentconfig.AgentTypeOpenAI,
			OpenAI: &agentconfig.LocalAPIConfig{
				APIKey: "test-key",
				Model:  "gpt-5",
			},
		},
	})
	if err != nil {
		t.Fatalf("validatePoolMembers() error = %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2", len(members))
	}
	if members[0].Name != "codex" || members[1].Name != "openai" {
		t.Fatalf("pool members = %#v, want codex then openai", members)
	}
}
