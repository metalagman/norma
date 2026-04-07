package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	initcmd "github.com/normahq/norma/cmd/norma/init"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const relayConfigFileName = "relay.yaml"

var (
	relayInitInput         io.Reader = os.Stdin
	relayInitOutput        io.Writer = os.Stdout
	relayInitIsInteractive           = defaultRelayInitIsInteractive
)

func initCommand() *cobra.Command {
	var relayRootAgent string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize relay config in the current repository",
		Long:  "Create .norma/relay.yaml with relay defaults and a selected relay.root_agent.",
		RunE: func(_ *cobra.Command, _ []string) error {
			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}

			normaDir := filepath.Join(workingDir, ".norma")
			if err := os.MkdirAll(normaDir, 0o700); err != nil {
				return fmt.Errorf("create .norma directory: %w", err)
			}

			configPath := filepath.Join(normaDir, relayConfigFileName)
			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("%s already exists", configPath)
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", configPath, err)
			}

			doc, agentIDs, err := buildRelayInitDocument()
			if err != nil {
				return err
			}

			selectedRootAgent, err := chooseRelayRootAgent(
				relayRootAgent,
				agentIDs,
				relayInitInput,
				relayInitOutput,
				relayInitIsInteractive(),
			)
			if err != nil {
				return err
			}

			if err := setRelayRootAgent(doc, selectedRootAgent); err != nil {
				return err
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

	cmd.Flags().StringVar(
		&relayRootAgent,
		"relay-root-agent",
		"",
		"relay root agent id (required in non-interactive mode)",
	)

	return cmd
}

func buildRelayInitDocument() (map[string]any, []string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(initcmd.DefaultConfigYAML), &doc); err != nil {
		return nil, nil, fmt.Errorf("parse default norma config template: %w", err)
	}
	if err := normalizeRelayInitAgentIDs(doc); err != nil {
		return nil, nil, err
	}

	var relayDefaults map[string]any
	if err := yaml.Unmarshal(defaultRelayConfig, &relayDefaults); err != nil {
		return nil, nil, fmt.Errorf("parse default relay config template: %w", err)
	}

	relaySection, ok := toStringAnyMap(relayDefaults["relay"])
	if !ok {
		return nil, nil, fmt.Errorf("default relay template is missing relay section")
	}
	ensureRelayMCPAddressDefault(relaySection)
	doc["relay"] = relaySection

	agentIDs, err := extractAgentIDs(doc)
	if err != nil {
		return nil, nil, err
	}
	return doc, agentIDs, nil
}

func normalizeRelayInitAgentIDs(doc map[string]any) error {
	normaSection, ok := toStringAnyMap(doc["norma"])
	if !ok {
		return fmt.Errorf("default template is missing norma section")
	}
	agents, ok := toStringAnyMap(normaSection["agents"])
	if !ok || len(agents) == 0 {
		return fmt.Errorf("default template does not define norma.agents")
	}

	renames := map[string]string{
		"gemini_acp_agent":      "gemini",
		"opencode_acp_agent":    "opencode",
		"codex_acp_agent":       "codex",
		"copilot_acp":           "copilot",
		"claude_code_acp_agent": "claude_code",
	}
	for oldID, newID := range renames {
		value, exists := agents[oldID]
		if !exists {
			continue
		}
		if _, collision := agents[newID]; collision {
			return fmt.Errorf("cannot rename default agent id %q to %q: target already exists", oldID, newID)
		}
		agents[newID] = value
		delete(agents, oldID)
	}
	normaSection["agents"] = agents
	doc["norma"] = normaSection

	replaceAgentReferencesInPlace(doc, renames, "")
	deleteAgentReferencesInPlace(doc, "custom_generic_acp_agent")
	deleteAgentReferencesInPlace(doc, "custom_generic")
	delete(agents, "custom_generic_acp_agent")
	delete(agents, "custom_generic")
	return nil
}

func replaceAgentReferencesInPlace(value any, renames map[string]string, parentKey string) any {
	switch v := value.(type) {
	case map[string]any:
		for key, entry := range v {
			v[key] = replaceAgentReferencesInPlace(entry, renames, key)
		}
		return v
	case []any:
		for i, entry := range v {
			v[i] = replaceAgentReferencesInPlace(entry, renames, parentKey)
		}
		return v
	case string:
		if parentKey == "type" {
			return v
		}
		if renamed, ok := renames[v]; ok {
			return renamed
		}
		return v
	default:
		return value
	}
}

func deleteAgentReferencesInPlace(value any, target string) {
	switch v := value.(type) {
	case map[string]any:
		for key, entry := range v {
			if key == "agents" {
				continue
			}
			switch typedEntry := entry.(type) {
			case string:
				if typedEntry == target {
					delete(v, key)
					continue
				}
			case []any:
				filtered := make([]any, 0, len(typedEntry))
				for _, item := range typedEntry {
					if s, ok := item.(string); ok && s == target {
						continue
					}
					filtered = append(filtered, item)
				}
				v[key] = filtered
				deleteAgentReferencesInPlace(filtered, target)
				continue
			}
			deleteAgentReferencesInPlace(entry, target)
		}
	case []any:
		for _, item := range v {
			deleteAgentReferencesInPlace(item, target)
		}
	}
}

func ensureRelayMCPAddressDefault(relaySection map[string]any) {
	defaultMCP := map[string]any{"address": ""}

	rawMCP, exists := relaySection["mcp"]
	if !exists {
		relaySection["mcp"] = defaultMCP
		return
	}
	mcpSection, ok := toStringAnyMap(rawMCP)
	if !ok {
		relaySection["mcp"] = defaultMCP
		return
	}
	if _, ok := mcpSection["address"]; !ok {
		mcpSection["address"] = ""
	}
	relaySection["mcp"] = mcpSection
}

func extractAgentIDs(doc map[string]any) ([]string, error) {
	normaSection, ok := toStringAnyMap(doc["norma"])
	if !ok {
		return nil, fmt.Errorf("default template is missing norma section")
	}

	agents, ok := toStringAnyMap(normaSection["agents"])
	if !ok || len(agents) == 0 {
		return nil, fmt.Errorf("default template does not define norma.agents")
	}

	ids := make([]string, 0, len(agents))
	for id := range agents {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		ids = append(ids, trimmedID)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("default template does not define usable norma agent ids")
	}

	sort.Strings(ids)
	return ids, nil
}

func chooseRelayRootAgent(provided string, agentIDs []string, in io.Reader, out io.Writer, interactive bool) (string, error) {
	selected := strings.TrimSpace(provided)
	if selected != "" {
		if !contains(agentIDs, selected) {
			return "", fmt.Errorf(
				"--relay-root-agent %q not found; available agent ids: %s",
				selected,
				strings.Join(agentIDs, ", "),
			)
		}
		return selected, nil
	}

	if !interactive {
		return "", fmt.Errorf(
			"--relay-root-agent is required in non-interactive mode; available agent ids: %s",
			strings.Join(agentIDs, ", "),
		)
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

	reader := bufio.NewReader(in)
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

func setRelayRootAgent(doc map[string]any, rootAgent string) error {
	relaySection, ok := toStringAnyMap(doc["relay"])
	if !ok {
		return fmt.Errorf("relay section is missing from generated config")
	}
	relaySection["root_agent"] = rootAgent
	doc["relay"] = relaySection
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
