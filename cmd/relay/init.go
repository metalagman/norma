package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/normahq/norma/internal/git"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	relayConfigFileName   = "config.yaml"
	relayConfigRelDir     = ".config/relay"
	relayRuntimeStatePath = ".config/relay"
)

const (
	relayInitCodexModel      = "gpt-5.3-codex"
	relayInitClaudeCodeModel = "claude-sonnet-4-6"
)

const relayInitSystemInstructionExample = "You are my relay agent.\nPrefer concise, actionable answers.\nWhen a request is ambiguous, ask one short clarifying question."

type relayInitAgentTemplate struct {
	ID           string
	Type         string
	Model        string
	DetectBinary []string
}

var relayInitAgentTemplates = []relayInitAgentTemplate{
	{ID: "codex", Type: "codex_acp", Model: relayInitCodexModel, DetectBinary: []string{"codex"}},
	{ID: "opencode", Type: "opencode_acp", Model: "opencode/big-pickle", DetectBinary: []string{"opencode"}},
	{ID: "copilot", Type: "copilot_acp", Model: "gpt-5-codex", DetectBinary: []string{"copilot"}},
	{ID: "gemini", Type: "gemini_acp", Model: "gemini-3-flash-preview", DetectBinary: []string{"gemini"}},
	{ID: "claude_code", Type: "claude_code_acp", Model: relayInitClaudeCodeModel, DetectBinary: []string{"claudecode", "claude"}},
}

var (
	relayInitInput         io.Reader = os.Stdin
	relayInitOutput        io.Writer = os.Stdout
	relayInitIsInteractive           = defaultRelayInitIsInteractive
	relayInitLookPath                = exec.LookPath
	relayInitCurrentBranch           = detectRelayInitCurrentBranch
)

func initCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize relay config in the current repository",
		Long:  "Create .config/relay/config.yaml with relay defaults and autodetected runtime agents.",
		RunE: func(_ *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}

			relayConfigDir := filepath.Join(workingDir, relayConfigRelDir)
			if err := os.MkdirAll(relayConfigDir, 0o700); err != nil {
				return fmt.Errorf("create relay config directory: %w", err)
			}

			configPath := filepath.Join(relayConfigDir, relayConfigFileName)
			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("%s already exists", configPath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", configPath, err)
			}
			if err := writeRelayConfigGitignore(relayConfigDir); err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Join(workingDir, relayRuntimeStatePath), 0o700); err != nil {
				return fmt.Errorf("create relay runtime state directory: %w", err)
			}

			doc, agentIDs, err := buildRelayInitDocument(workingDir)
			if err != nil {
				return err
			}

			interactive := relayInitIsInteractive()
			inputReader := bufio.NewReader(relayInitInput)
			selectedRootAgent, err := chooseRelayRootAgent(agentIDs, inputReader, relayInitOutput, interactive)
			if err != nil {
				return err
			}

			if err := setRelayRootAgent(doc, selectedRootAgent); err != nil {
				return err
			}
			if err := setRelayAgentSystemInstructionExample(doc, selectedRootAgent); err != nil {
				return err
			}
			if interactive {
				telegramToken, promptErr := promptRelayTelegramToken(inputReader, relayInitOutput)
				if promptErr != nil {
					return promptErr
				}
				if err := setRelayTelegramToken(doc, telegramToken); err != nil {
					return err
				}
			}

			content, err := yaml.Marshal(doc)
			if err != nil {
				return fmt.Errorf("marshal relay config: %w", err)
			}

			if err := os.WriteFile(configPath, content, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", configPath, err)
			}

			_, _ = fmt.Fprintf(relayInitOutput, "relay initialized successfully\n")
			_, _ = fmt.Fprintf(relayInitOutput, "config: %s\n", configPath)
			_, _ = fmt.Fprintf(relayInitOutput, "root agent: %s\n", selectedRootAgent)

			return nil
		},
	}

	return cmd
}

func writeRelayConfigGitignore(configDir string) error {
	gitignorePath := filepath.Join(configDir, ".gitignore")
	content := []byte("*\n!.gitignore\n!config.yaml\n")
	if err := os.WriteFile(gitignorePath, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", gitignorePath, err)
	}
	return nil
}

func buildRelayInitDocument(workingDir string) (map[string]any, []string, error) {
	detectedAgents := detectRelayInitAgents()
	if len(detectedAgents) == 0 {
		return nil, nil, fmt.Errorf(
			"no supported agent CLI detected in PATH; install at least one of: codex, opencode, copilot, gemini, claudecode/claude",
		)
	}

	var relayDefaults map[string]any
	if err := yaml.Unmarshal(defaultRelayConfig, &relayDefaults); err != nil {
		return nil, nil, fmt.Errorf("parse default relay config template: %w", err)
	}

	relaySection, ok := toStringAnyMap(relayDefaults["relay"])
	if !ok {
		return nil, nil, fmt.Errorf("default relay template is missing relay section")
	}
	ensureRelayMCPServersDefault(relaySection)
	relayBaseBranch, err := relayInitCurrentBranch(workingDir)
	if err != nil {
		relayBaseBranch = ""
	}
	if err := setRelayWorkspaceBaseBranch(relaySection, relayBaseBranch); err != nil {
		return nil, nil, err
	}

	agentIDs := make([]string, 0, len(detectedAgents))
	for _, detected := range detectedAgents {
		agentIDs = append(agentIDs, detected.ID)
	}

	doc := map[string]any{
		"norma": map[string]any{
			"agents":      buildRelayInitAgents(detectedAgents),
			"mcp_servers": map[string]any{},
		},
		"relay":    relaySection,
		"profiles": buildRelayInitProfiles(agentIDs),
	}

	return doc, agentIDs, nil
}

func detectRelayInitCurrentBranch(workingDir string) (string, error) {
	return git.CurrentBranch(context.Background(), workingDir)
}

func detectRelayInitAgents() []relayInitAgentTemplate {
	detected := make([]relayInitAgentTemplate, 0, len(relayInitAgentTemplates))
	for _, template := range relayInitAgentTemplates {
		for _, binary := range template.DetectBinary {
			if _, err := relayInitLookPath(binary); err == nil {
				detected = append(detected, template)
				break
			}
		}
	}
	return detected
}

func buildRelayInitAgents(detected []relayInitAgentTemplate) map[string]any {
	agents := make(map[string]any, len(detected)+1)
	poolMembers := make([]any, 0, len(detected))

	for _, agentTemplate := range detected {
		agentBlock := map[string]any{"type": agentTemplate.Type}
		typeConfig := map[string]any{}
		if strings.TrimSpace(agentTemplate.Model) != "" {
			typeConfig["model"] = agentTemplate.Model
		}
		agentBlock[agentTemplate.Type] = typeConfig
		agents[agentTemplate.ID] = agentBlock
		poolMembers = append(poolMembers, agentTemplate.ID)
	}

	agents["pool"] = map[string]any{
		"type": "pool",
		"pool": map[string]any{
			"members": poolMembers,
		},
	}

	return agents
}

func buildRelayInitProfiles(agentIDs []string) map[string]any {
	profiles := make(map[string]any, len(agentIDs)+1)
	if len(agentIDs) == 0 {
		return profiles
	}

	profiles["default"] = map[string]any{
		"relay": map[string]any{
			"root_agent": agentIDs[0],
		},
	}
	for _, id := range agentIDs {
		profiles[id] = map[string]any{
			"relay": map[string]any{
				"root_agent": id,
			},
		}
	}

	return profiles
}

func ensureRelayMCPServersDefault(relaySection map[string]any) {
	raw, exists := relaySection["mcp_servers"]
	if !exists || raw == nil {
		relaySection["mcp_servers"] = []any{}
		return
	}
	if _, ok := raw.([]any); ok {
		return
	}
	if _, ok := raw.([]string); ok {
		return
	}
	relaySection["mcp_servers"] = []any{}
}

func chooseRelayRootAgent(agentIDs []string, in io.Reader, out io.Writer, interactive bool) (string, error) {
	if len(agentIDs) == 0 {
		return "", fmt.Errorf("no agent ids are available for relay.root_agent selection")
	}
	if !interactive {
		return agentIDs[0], nil
	}
	return promptRelayRootAgent(agentIDs, in, out)
}

func promptRelayRootAgent(agentIDs []string, in io.Reader, out io.Writer) (string, error) {
	if len(agentIDs) == 0 {
		return "", fmt.Errorf("no agent ids are available for relay.root_agent selection")
	}

	_, _ = fmt.Fprintln(out, "Select relay.root_agent:")
	for i, id := range agentIDs {
		_, _ = fmt.Fprintf(out, "  %d) %s\n", i+1, id)
	}
	_, _ = fmt.Fprintf(out, "Enter number or agent id [1]: ")

	reader := asBufferedReader(in)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read relay.root_agent selection: %w", err)
		}

		value := strings.TrimSpace(line)
		if value == "" {
			return agentIDs[0], nil
		}

		if idx, parseErr := strconv.Atoi(value); parseErr == nil && idx >= 1 && idx <= len(agentIDs) {
			return agentIDs[idx-1], nil
		}

		if contains(agentIDs, value) {
			return value, nil
		}

		if err == io.EOF {
			return "", fmt.Errorf("invalid relay.root_agent selection %q", value)
		}

		_, _ = fmt.Fprintf(
			out,
			"Invalid selection %q. Enter number 1-%d or one of: %s\n",
			value,
			len(agentIDs),
			strings.Join(agentIDs, ", "),
		)
		_, _ = fmt.Fprintf(out, "Enter number or agent id [1]: ")
	}
}

func promptRelayTelegramToken(in io.Reader, out io.Writer) (string, error) {
	_, _ = fmt.Fprintln(out, "Enter Telegram bot token (optional, press Enter to skip): ")
	reader := asBufferedReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read relay.telegram.token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func setRelayRootAgent(doc map[string]any, rootAgent string) error {
	relaySection, ok := toStringAnyMap(doc["relay"])
	if !ok {
		return fmt.Errorf("relay section is missing from generated config")
	}
	relaySection["root_agent"] = rootAgent
	doc["relay"] = relaySection

	profilesSection, ok := toStringAnyMap(doc["profiles"])
	if !ok {
		return nil
	}
	defaultProfile, ok := toStringAnyMap(profilesSection["default"])
	if !ok {
		return nil
	}
	relayProfile, ok := toStringAnyMap(defaultProfile["relay"])
	if !ok {
		return nil
	}
	relayProfile["root_agent"] = rootAgent
	defaultProfile["relay"] = relayProfile
	profilesSection["default"] = defaultProfile
	doc["profiles"] = profilesSection

	return nil
}

func setRelayTelegramToken(doc map[string]any, token string) error {
	relaySection, ok := toStringAnyMap(doc["relay"])
	if !ok {
		return fmt.Errorf("relay section is missing from generated config")
	}
	telegramSection, ok := toStringAnyMap(relaySection["telegram"])
	if !ok {
		return fmt.Errorf("relay.telegram section is missing from generated config")
	}
	telegramSection["token"] = token
	relaySection["telegram"] = telegramSection
	doc["relay"] = relaySection
	return nil
}

func setRelayAgentSystemInstructionExample(doc map[string]any, rootAgent string) error {
	agentID := strings.TrimSpace(rootAgent)
	if agentID == "" {
		return nil
	}

	relaySection, ok := toStringAnyMap(doc["relay"])
	if !ok {
		return fmt.Errorf("relay section is missing from generated config")
	}

	agentInstructions := map[string]any{}
	if raw, exists := relaySection["agent_system_instructions"]; exists && raw != nil {
		if existing, ok := toStringAnyMap(raw); ok {
			agentInstructions = existing
		}
	}

	if existing, exists := agentInstructions[agentID]; !exists || strings.TrimSpace(fmt.Sprintf("%v", existing)) == "" {
		agentInstructions[agentID] = relayInitSystemInstructionExample
	}

	relaySection["agent_system_instructions"] = agentInstructions
	doc["relay"] = relaySection
	return nil
}

func setRelayWorkspaceBaseBranch(relaySection map[string]any, baseBranch string) error {
	workspaceSection, ok := toStringAnyMap(relaySection["workspace"])
	if !ok {
		return fmt.Errorf("relay.workspace section is missing from generated config")
	}
	workspaceSection["base_branch"] = strings.TrimSpace(baseBranch)
	relaySection["workspace"] = workspaceSection
	return nil
}

func toStringAnyMap(raw any) (map[string]any, bool) {
	switch v := raw.(type) {
	case map[string]any:
		return v, true
	case map[any]any:
		m := make(map[string]any, len(v))
		for key, value := range v {
			k, ok := key.(string)
			if !ok {
				return nil, false
			}
			m[k] = value
		}
		return m, true
	default:
		return nil, false
	}
}

func asBufferedReader(in io.Reader) *bufio.Reader {
	if reader, ok := in.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(in)
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func defaultRelayInitIsInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
