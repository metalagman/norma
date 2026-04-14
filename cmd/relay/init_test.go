package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/normahq/norma/pkg/runtime/appconfig"
	"gopkg.in/yaml.v3"
)

const (
	testRelayRootAgentCodex    = "codex"
	testRelayRootAgentOpencode = "opencode"
	testRelayRootAgentCopilot  = "copilot"
)

func TestInitCommand_NonInteractiveAutoSelectsRootAndGeneratesDetectedAgents(t *testing.T) {
	workingDir := setWorkingDir(t)
	setDetectedBinaries(t, "codex", "opencode", "copilot", "gemini", "claude")
	setDetectedBaseBranch(t, "main", nil)

	prevInput := relayInitInput
	prevOutput := relayInitOutput
	prevInteractive := relayInitIsInteractive
	t.Cleanup(func() {
		relayInitInput = prevInput
		relayInitOutput = prevOutput
		relayInitIsInteractive = prevInteractive
	})

	relayInitInput = strings.NewReader("")
	relayInitOutput = &bytes.Buffer{}
	relayInitIsInteractive = func() bool { return false }

	cmd := initCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	assertRelayInitArtifacts(t, workingDir)

	doc := mustReadRelayDoc(t, workingDir)
	assertNoCLISection(t, doc)

	relaySection, ok := toStringAnyMap(doc["relay"])
	if !ok {
		t.Fatal("relay section missing in generated config")
	}
	if got := relaySection["root_agent"]; got != testRelayRootAgentCodex {
		t.Fatalf("relay.root_agent = %#v, want %s", got, testRelayRootAgentCodex)
	}
	telegramSection, ok := toStringAnyMap(relaySection["telegram"])
	if !ok {
		t.Fatal("relay.telegram section missing in generated config")
	}
	if got := telegramSection["token"]; got != "" {
		t.Fatalf("relay.telegram.token = %#v, want empty string in non-interactive mode", got)
	}
	rawRelayMCPServers, ok := relaySection["mcp_servers"].([]any)
	if !ok {
		t.Fatalf("relay.mcp_servers type = %T, want []any", relaySection["mcp_servers"])
	}
	if len(rawRelayMCPServers) != 0 {
		t.Fatalf("relay.mcp_servers = %#v, want empty", rawRelayMCPServers)
	}
	workspaceSection, ok := toStringAnyMap(relaySection["workspace"])
	if !ok {
		t.Fatal("relay.workspace section missing in generated config")
	}
	if got := workspaceSection["base_branch"]; got != "main" {
		t.Fatalf("relay.workspace.base_branch = %#v, want main", got)
	}
	assertRelaySystemInstructionExample(t, relaySection)

	normaSection, ok := toStringAnyMap(doc["norma"])
	if !ok {
		t.Fatal("norma section missing in generated config")
	}
	assertMapHasOnlyKeys(t, normaSection, []string{"providers", "mcp_servers"})
	providers, ok := toStringAnyMap(normaSection["providers"])
	if !ok {
		t.Fatal("norma.providers missing in generated config")
	}
	mcpServers, ok := toStringAnyMap(normaSection["mcp_servers"])
	if !ok {
		t.Fatal("norma.mcp_servers missing in generated config")
	}
	if len(mcpServers) != 0 {
		t.Fatalf("norma.mcp_servers = %#v, want empty map", mcpServers)
	}
	assertMapHasOnlyKeys(t, providers, []string{"codex", "opencode", "copilot", "gemini", "claude_code", "pool"})
	assertAgentModel(t, providers, "codex", "codex_acp", relayInitCodexModel)
	assertAgentModel(t, providers, "claude_code", "claude_code_acp", relayInitClaudeCodeModel)

	poolMembers := readPoolMembers(t, providers)
	wantMembers := []string{"codex", "opencode", "copilot", "gemini", "claude_code"}
	if !reflect.DeepEqual(poolMembers, wantMembers) {
		t.Fatalf("pool.members = %#v, want %#v", poolMembers, wantMembers)
	}

	profiles, ok := toStringAnyMap(doc["profiles"])
	if !ok {
		t.Fatal("profiles section missing in generated config")
	}
	assertMapHasOnlyKeys(t, profiles, []string{"codex", "opencode", "copilot", "gemini", "claude_code"})
	assertProfileRoot(t, profiles, "codex", "codex")
	assertProfileRoot(t, profiles, "opencode", "opencode")
	assertProfileRoot(t, profiles, "copilot", "copilot")
	assertProfileRoot(t, profiles, "gemini", "gemini")
	assertProfileRoot(t, profiles, "claude_code", "claude_code")

	if _, ok := profiles["pool"]; ok {
		t.Fatal("profiles.pool must not be generated")
	}
}

func TestInitCommand_InteractiveSelectionAndToken(t *testing.T) {
	workingDir := setWorkingDir(t)
	setDetectedBinaries(t, "codex", "opencode", "gemini")
	setDetectedBaseBranch(t, "", fmt.Errorf("not a git repo"))

	prevInput := relayInitInput
	prevOutput := relayInitOutput
	prevInteractive := relayInitIsInteractive
	t.Cleanup(func() {
		relayInitInput = prevInput
		relayInitOutput = prevOutput
		relayInitIsInteractive = prevInteractive
	})

	relayInitInput = strings.NewReader("2\nmy-token\n")
	relayInitOutput = &bytes.Buffer{}
	relayInitIsInteractive = func() bool { return true }

	cmd := initCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	doc := mustReadRelayDoc(t, workingDir)
	relaySection := mustMap(t, doc, "relay")
	if got := relaySection["root_agent"]; got != testRelayRootAgentOpencode {
		t.Fatalf("relay.root_agent = %#v, want %s", got, testRelayRootAgentOpencode)
	}
	assertRelaySystemInstructionExample(t, relaySection)
	telegramSection := mustMap(t, relaySection, "telegram")
	if got := telegramSection["token"]; got != "my-token" {
		t.Fatalf("relay.telegram.token = %#v, want my-token", got)
	}
	profiles := mustMap(t, doc, "profiles")
	if _, ok := profiles["default"]; ok {
		t.Fatal("profiles.default must not be generated")
	}
	assertProfileRoot(t, profiles, "opencode", "opencode")
}

func TestInitCommand_InteractiveDefaultPrioritizesCopilotBeforeGemini(t *testing.T) {
	workingDir := setWorkingDir(t)
	setDetectedBinaries(t, "copilot", "gemini")
	setDetectedBaseBranch(t, "", fmt.Errorf("not a git repo"))

	prevInput := relayInitInput
	prevOutput := relayInitOutput
	prevInteractive := relayInitIsInteractive
	t.Cleanup(func() {
		relayInitInput = prevInput
		relayInitOutput = prevOutput
		relayInitIsInteractive = prevInteractive
	})

	relayInitInput = strings.NewReader("\n\n")
	relayInitOutput = &bytes.Buffer{}
	relayInitIsInteractive = func() bool { return true }

	cmd := initCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	doc := mustReadRelayDoc(t, workingDir)
	relaySection := mustMap(t, doc, "relay")
	if got := relaySection["root_agent"]; got != testRelayRootAgentCopilot {
		t.Fatalf("relay.root_agent = %#v, want %s", got, testRelayRootAgentCopilot)
	}
	assertRelaySystemInstructionExample(t, relaySection)
}

func TestInitCommand_FailsWhenNoSupportedAgentCLIFound(t *testing.T) {
	_ = setWorkingDir(t)
	setDetectedBinaries(t)
	setDetectedBaseBranch(t, "", fmt.Errorf("not a git repo"))

	prevInput := relayInitInput
	prevOutput := relayInitOutput
	prevInteractive := relayInitIsInteractive
	t.Cleanup(func() {
		relayInitInput = prevInput
		relayInitOutput = prevOutput
		relayInitIsInteractive = prevInteractive
	})

	relayInitInput = strings.NewReader("")
	relayInitOutput = &bytes.Buffer{}
	relayInitIsInteractive = func() bool { return false }

	cmd := initCommand()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no supported agent CLI is detected")
	}
	if !strings.Contains(err.Error(), "no supported agent CLI detected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitCommand_FailsWhenConfigAlreadyExists(t *testing.T) {
	workingDir := setWorkingDir(t)
	setDetectedBinaries(t, "codex")
	setDetectedBaseBranch(t, "", fmt.Errorf("not a git repo"))

	configPath := filepath.Join(workingDir, relayConfigRelDir, relayConfigFileName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("relay:\n  root_agent: existing\n"), 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	cmd := initCommand()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when relay config already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitCommand_RemovedRelayRootAgentFlagRejected(t *testing.T) {
	_ = setWorkingDir(t)
	setDetectedBinaries(t, "codex")
	setDetectedBaseBranch(t, "", fmt.Errorf("not a git repo"))

	cmd := initCommand()
	cmd.SetArgs([]string{"--relay-root-agent", "codex"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown flag error for removed --relay-root-agent")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("error = %q, want unknown flag", err.Error())
	}
}

func TestChooseRelayRootAgent_NonInteractivePicksTopPriority(t *testing.T) {
	got, err := chooseRelayRootAgent([]string{"codex", "opencode"}, strings.NewReader(""), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("chooseRelayRootAgent: %v", err)
	}
	if got != "codex" {
		t.Fatalf("selected = %q, want codex", got)
	}
}

func TestChooseRelayRootAgent_InteractiveSelectionByNumber(t *testing.T) {
	var out bytes.Buffer
	got, err := chooseRelayRootAgent([]string{"alpha", "beta"}, strings.NewReader("2\n"), &out, true)
	if err != nil {
		t.Fatalf("chooseRelayRootAgent: %v", err)
	}
	if got != "beta" {
		t.Fatalf("selected = %q, want beta", got)
	}
}

func TestInitCommand_GeneratedConfigLoadableByRelayLoader(t *testing.T) {
	workingDir := setWorkingDir(t)
	setDetectedBinaries(t, "codex")
	setDetectedBaseBranch(t, "main", nil)

	prevInput := relayInitInput
	prevOutput := relayInitOutput
	prevInteractive := relayInitIsInteractive
	t.Cleanup(func() {
		relayInitInput = prevInput
		relayInitOutput = prevOutput
		relayInitIsInteractive = prevInteractive
	})
	relayInitInput = strings.NewReader("")
	relayInitOutput = &bytes.Buffer{}
	relayInitIsInteractive = func() bool { return false }

	cmd := initCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init command failed: %v", err)
	}

	var doc relayTestConfigDocument
	selectedProfile, err := appconfig.LoadConfigDocument(
		appconfig.RuntimeLoadOptions{WorkingDir: workingDir},
		appconfig.AppLoadOptions{
			AppName:      "relay",
			DefaultsYAML: defaultRelayConfig,
		},
		&doc,
	)
	if err != nil {
		t.Fatalf("LoadConfigDocument: %v", err)
	}
	if selectedProfile != "default" {
		t.Fatalf("selected profile = %q, want default", selectedProfile)
	}
	if got := doc.Relay.RootAgent; got != testRelayRootAgentCodex {
		t.Fatalf("doc.Relay.RootAgent = %q, want %s", got, testRelayRootAgentCodex)
	}
}

func setWorkingDir(t *testing.T) string {
	t.Helper()
	workingDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return workingDir
}

func setDetectedBinaries(t *testing.T, binaries ...string) {
	t.Helper()
	prevLookPath := relayInitLookPath
	t.Cleanup(func() {
		relayInitLookPath = prevLookPath
	})

	present := make(map[string]struct{}, len(binaries))
	for _, name := range binaries {
		present[strings.TrimSpace(name)] = struct{}{}
	}
	relayInitLookPath = func(file string) (string, error) {
		if _, ok := present[file]; ok {
			return "/usr/bin/" + file, nil
		}
		return "", fmt.Errorf("%s not found", file)
	}
}

func setDetectedBaseBranch(t *testing.T, branch string, branchErr error) {
	t.Helper()
	prev := relayInitCurrentBranch
	t.Cleanup(func() {
		relayInitCurrentBranch = prev
	})
	relayInitCurrentBranch = func(string) (string, error) {
		return branch, branchErr
	}
}

func mustReadRelayDoc(t *testing.T, workingDir string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(workingDir, relayConfigRelDir, relayConfigFileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return doc
}

func assertRelayInitArtifacts(t *testing.T, workingDir string) {
	t.Helper()

	gitignorePath := filepath.Join(workingDir, relayConfigRelDir, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read %s: %v", gitignorePath, err)
	}
	if got, want := string(content), "*\n!.gitignore\n!config.yaml\n"; got != want {
		t.Fatalf("%s content = %q, want %q", gitignorePath, got, want)
	}

	stateDir := filepath.Join(workingDir, relayRuntimeStatePath)
	info, err := os.Stat(stateDir)
	if err != nil {
		t.Fatalf("stat %s: %v", stateDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", stateDir)
	}
}

func mustMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	raw, ok := parent[key]
	if !ok {
		t.Fatalf("missing key %q", key)
	}
	m, ok := toStringAnyMap(raw)
	if !ok {
		t.Fatalf("%s is not a map", key)
	}
	return m
}

func assertNoCLISection(t *testing.T, doc map[string]any) {
	t.Helper()
	if _, ok := doc["cli"]; ok {
		t.Fatal("top-level cli section must not be generated by relay init")
	}
	profiles, ok := toStringAnyMap(doc["profiles"])
	if !ok {
		t.Fatal("profiles section missing in generated config")
	}
	for profileName, raw := range profiles {
		profile, ok := toStringAnyMap(raw)
		if !ok {
			t.Fatalf("profiles.%s is not a map", profileName)
		}
		if _, hasCLI := profile["cli"]; hasCLI {
			t.Fatalf("profiles.%s.cli must not be generated", profileName)
		}
	}
}

func assertMapHasOnlyKeys(t *testing.T, m map[string]any, expected []string) {
	t.Helper()
	want := make(map[string]struct{}, len(expected))
	for _, key := range expected {
		want[key] = struct{}{}
	}
	if len(m) != len(expected) {
		t.Fatalf("map keys = %v, want %v", sortedKeys(m), expected)
	}
	for key := range m {
		if _, ok := want[key]; !ok {
			t.Fatalf("unexpected key %q in map; keys=%v", key, sortedKeys(m))
		}
	}
}

func assertAgentModel(t *testing.T, providers map[string]any, id, typeName, wantModel string) {
	t.Helper()
	agent := mustMap(t, providers, id)
	if got := agent["type"]; got != typeName {
		t.Fatalf("norma.providers.%s.type = %#v, want %s", id, got, typeName)
	}
	typeBlock := mustMap(t, agent, typeName)
	if got := typeBlock["model"]; got != wantModel {
		t.Fatalf("norma.providers.%s.%s.model = %#v, want %s", id, typeName, got, wantModel)
	}
}

func readPoolMembers(t *testing.T, providers map[string]any) []string {
	t.Helper()
	poolAgent := mustMap(t, providers, "pool")
	if got := poolAgent["type"]; got != "pool" {
		t.Fatalf("norma.providers.pool.type = %#v, want pool", got)
	}
	poolCfg := mustMap(t, poolAgent, "pool")
	rawMembers, ok := poolCfg["members"].([]any)
	if !ok {
		t.Fatalf("norma.providers.pool.pool.members type = %T, want []any", poolCfg["members"])
	}
	members := make([]string, 0, len(rawMembers))
	for _, raw := range rawMembers {
		member, ok := raw.(string)
		if !ok {
			t.Fatalf("pool member type = %T, want string", raw)
		}
		members = append(members, member)
	}
	return members
}

func assertProfileRoot(t *testing.T, profiles map[string]any, profileName, wantRoot string) {
	t.Helper()
	profile := mustMap(t, profiles, profileName)
	relayProfile := mustMap(t, profile, "relay")
	if got := relayProfile["root_agent"]; got != wantRoot {
		t.Fatalf("profiles.%s.relay.root_agent = %#v, want %s", profileName, got, wantRoot)
	}
}

func assertRelaySystemInstructionExample(t *testing.T, relaySection map[string]any) {
	t.Helper()
	rawPrompt, ok := relaySection["system_instructions"]
	if !ok {
		t.Fatalf("relay.system_instructions key is missing")
	}
	prompt, ok := rawPrompt.(string)
	if !ok {
		t.Fatalf("relay.system_instructions type = %T, want string", rawPrompt)
	}
	if strings.TrimSpace(prompt) == "" {
		t.Fatalf("relay.system_instructions is empty")
	}
	if prompt != relayInitSystemInstructionExample {
		t.Fatalf("relay.system_instructions = %q, want %q", prompt, relayInitSystemInstructionExample)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
