package mcpinspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

// RunConfig describes how MCP inspection should run.
type RunConfig struct {
	Command      []string
	WorkingDir   string
	Component    string
	StartMessage string
	JSONOutput   bool
	LogLevel     zerolog.Level
	Stdout       io.Writer
	Stderr       io.Writer
}

type methodSupport struct {
	Tools             bool `json:"tools"`
	Prompts           bool `json:"prompts"`
	Resources         bool `json:"resources"`
	ResourceTemplates bool `json:"resource_templates"`
}

type inspectOutput struct {
	Initialize        *mcp.InitializeResult   `json:"initialize,omitempty"`
	Tools             []*mcp.Tool             `json:"tools,omitempty"`
	Prompts           []*mcp.Prompt           `json:"prompts,omitempty"`
	Resources         []*mcp.Resource         `json:"resources,omitempty"`
	ResourceTemplates []*mcp.ResourceTemplate `json:"resource_templates,omitempty"`

	Support methodSupport `json:"-"`
}

// Run connects to an MCP server command and prints initialize/capability details.
func Run(ctx context.Context, cfg RunConfig) error {
	if len(cfg.Command) == 0 {
		return fmt.Errorf("mcp server command is required")
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}

	component := strings.TrimSpace(cfg.Component)
	if component == "" {
		component = "mcp.inspect"
	}
	startMessage := strings.TrimSpace(cfg.StartMessage)
	if startMessage == "" {
		startMessage = "inspecting MCP server"
	}

	lockedStderr := &syncWriter{writer: cfg.Stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		Level(cfg.LogLevel).
		With().Timestamp().Str("component", component).Logger()

	logger.Info().
		Str("working_dir", cfg.WorkingDir).
		Strs("command", cfg.Command).
		Msg(startMessage)

	output, err := inspectMCPServer(ctx, cfg.WorkingDir, cfg.Command, lockedStderr, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to inspect MCP server")
		return err
	}

	if cfg.JSONOutput {
		return writeInspectJSON(cfg.Stdout, output)
	}
	return writeInspectHuman(cfg.Stdout, output)
}

func inspectMCPServer(ctx context.Context, workingDir string, command []string, stderr io.Writer, logger zerolog.Logger) (*inspectOutput, error) {
	return inspectMCPServerRaw(ctx, workingDir, command, stderr, logger)
}

func inspectMCPServerRaw(ctx context.Context, workingDir string, command []string, stderr io.Writer, logger zerolog.Logger) (*inspectOutput, error) {
	transport, err := newMCPCommandTransport(ctx, workingDir, command, stderr)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "norma-mcp-inspector", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to mcp server: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			logger.Warn().Err(closeErr).Msg("failed to close MCP session")
		}
	}()

	output := &inspectOutput{}
	output.Initialize = session.InitializeResult()
	output.Support = extractSupportFromInitialize(output.Initialize)

	if output.Support.Tools {
		output.Tools, output.Support.Tools, err = collectTools(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
	}
	if output.Support.Prompts {
		output.Prompts, output.Support.Prompts, err = collectPrompts(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list prompts: %w", err)
		}
	}
	if output.Support.Resources {
		output.Resources, output.Support.Resources, err = collectResources(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list resources: %w", err)
		}
	}
	if output.Support.ResourceTemplates {
		output.ResourceTemplates, output.Support.ResourceTemplates, err = collectResourceTemplates(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list resource templates: %w", err)
		}
	}
	return output, nil
}

func newMCPCommandTransport(ctx context.Context, workingDir string, command []string, stderr io.Writer) (*mcp.CommandTransport, error) {
	if len(command) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = workingDir
	cmd.Stderr = stderr
	return &mcp.CommandTransport{Command: cmd}, nil
}

func collectTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, bool, error) {
	tools := make([]*mcp.Tool, 0)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			if isMethodUnsupported(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if tool != nil {
			tools = append(tools, tool)
		}
	}
	return tools, true, nil
}

func collectPrompts(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Prompt, bool, error) {
	prompts := make([]*mcp.Prompt, 0)
	for prompt, err := range session.Prompts(ctx, nil) {
		if err != nil {
			if isMethodUnsupported(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if prompt != nil {
			prompts = append(prompts, prompt)
		}
	}
	return prompts, true, nil
}

func collectResources(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Resource, bool, error) {
	resources := make([]*mcp.Resource, 0)
	for resource, err := range session.Resources(ctx, nil) {
		if err != nil {
			if isMethodUnsupported(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if resource != nil {
			resources = append(resources, resource)
		}
	}
	return resources, true, nil
}

func collectResourceTemplates(ctx context.Context, session *mcp.ClientSession) ([]*mcp.ResourceTemplate, bool, error) {
	templates := make([]*mcp.ResourceTemplate, 0)
	for resourceTemplate, err := range session.ResourceTemplates(ctx, nil) {
		if err != nil {
			if isMethodUnsupported(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if resourceTemplate != nil {
			templates = append(templates, resourceTemplate)
		}
	}
	return templates, true, nil
}

func writeInspectJSON(stdout io.Writer, output *inspectOutput) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func writeInspectHuman(stdout io.Writer, output *inspectOutput) error {
	if output.Initialize == nil {
		if _, err := fmt.Fprintln(stdout, "Connected to MCP server, but initialize result is empty."); err != nil {
			return err
		}
	} else {
		serverName := ""
		serverVersion := ""
		if output.Initialize.ServerInfo != nil {
			serverName = strings.TrimSpace(output.Initialize.ServerInfo.Name)
			serverVersion = strings.TrimSpace(output.Initialize.ServerInfo.Version)
		}
		if _, err := fmt.Fprintf(stdout, "Server: %s %s\n", serverName, serverVersion); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "Protocol: %s\n", output.Initialize.ProtocolVersion); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "Capabilities: %s\n", formatServerCapabilities(output.Initialize.Capabilities)); err != nil {
			return err
		}
		if instructions := strings.TrimSpace(output.Initialize.Instructions); instructions != "" {
			if _, err := fmt.Fprintf(stdout, "Instructions: %s\n", instructions); err != nil {
				return err
			}
		}
	}

	if err := writeToolSummary(stdout, output.Support.Tools, output.Tools); err != nil {
		return err
	}
	if err := writePromptSummary(stdout, output.Support.Prompts, output.Prompts); err != nil {
		return err
	}
	if err := writeResourceSummary(stdout, output.Support.Resources, output.Resources); err != nil {
		return err
	}
	if err := writeResourceTemplateSummary(stdout, output.Support.ResourceTemplates, output.ResourceTemplates); err != nil {
		return err
	}
	return nil
}

func writeToolSummary(stdout io.Writer, supported bool, tools []*mcp.Tool) error {
	items := make([]summaryItem, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		item := summaryItem{name: tool.Name, description: tool.Description}
		if tool.InputSchema != nil {
			if schemaText := marshalIndented(tool.InputSchema, "  Parameters: "); schemaText != "" {
				item.details = append(item.details, schemaText)
			}
		}
		if tool.OutputSchema != nil {
			if schemaText := marshalIndented(tool.OutputSchema, "  Response: "); schemaText != "" {
				item.details = append(item.details, schemaText)
			}
		}
		items = append(items, item)
	}
	return writeSummaryList(stdout, "Tools", supported, items)
}

func writePromptSummary(stdout io.Writer, supported bool, prompts []*mcp.Prompt) error {
	items := make([]summaryItem, 0, len(prompts))
	for _, prompt := range prompts {
		if prompt == nil {
			continue
		}
		items = append(items, summaryItem{name: prompt.Name, description: prompt.Description})
	}
	return writeSummaryList(stdout, "Prompts", supported, items)
}

func writeResourceSummary(stdout io.Writer, supported bool, resources []*mcp.Resource) error {
	items := make([]summaryItem, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		items = append(items, summaryItem{name: resource.URI, description: resource.Description})
	}
	return writeSummaryList(stdout, "Resources", supported, items)
}

func writeResourceTemplateSummary(stdout io.Writer, supported bool, templates []*mcp.ResourceTemplate) error {
	items := make([]summaryItem, 0, len(templates))
	for _, resourceTemplate := range templates {
		if resourceTemplate == nil {
			continue
		}
		items = append(items, summaryItem{name: resourceTemplate.URITemplate, description: resourceTemplate.Description})
	}
	return writeSummaryList(stdout, "Resource templates", supported, items)
}

func marshalIndented(value any, prefix string) string {
	text, err := json.MarshalIndent(value, "    ", "  ")
	if err != nil {
		return ""
	}
	return prefix + string(text)
}

func isMethodUnsupported(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "-32601") || strings.Contains(errText, "method not found")
}

func formatServerCapabilities(capabilities *mcp.ServerCapabilities) string {
	if capabilities == nil {
		return "(none)"
	}
	parts := make([]string, 0, 5)
	parts = append(parts, fmt.Sprintf("tools=%t", capabilities.Tools != nil))
	parts = append(parts, fmt.Sprintf("prompts=%t", capabilities.Prompts != nil))
	parts = append(parts, fmt.Sprintf("resources=%t", capabilities.Resources != nil))
	parts = append(parts, fmt.Sprintf("logging=%t", capabilities.Logging != nil))
	if len(capabilities.Experimental) > 0 {
		parts = append(parts, "experimental=true")
	}
	return strings.Join(parts, " ")
}

func extractSupportFromInitialize(initialize *mcp.InitializeResult) methodSupport {
	if initialize == nil || initialize.Capabilities == nil {
		return methodSupport{
			Tools:             true,
			Prompts:           true,
			Resources:         true,
			ResourceTemplates: true,
		}
	}
	return methodSupport{
		Tools:             initialize.Capabilities.Tools != nil,
		Prompts:           initialize.Capabilities.Prompts != nil,
		Resources:         initialize.Capabilities.Resources != nil,
		ResourceTemplates: initialize.Capabilities.Resources != nil,
	}
}

type summaryItem struct {
	name        string
	description string
	details     []string
}

func writeSummaryList(stdout io.Writer, label string, supported bool, items []summaryItem) error {
	if !supported {
		_, err := fmt.Fprintf(stdout, "%s: unsupported\n", label)
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%s (%d):\n", label, len(items)); err != nil {
		return err
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(stdout, "- (none)")
		return err
	}
	for _, item := range items {
		if _, err := fmt.Fprintf(stdout, "- %s", item.name); err != nil {
			return err
		}
		if strings.TrimSpace(item.description) != "" {
			if _, err := fmt.Fprintf(stdout, ": %s", item.description); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(stdout); err != nil {
			return err
		}
		for _, detail := range item.details {
			if strings.TrimSpace(detail) == "" {
				continue
			}
			if _, err := fmt.Fprintln(stdout, detail); err != nil {
				return err
			}
		}
	}
	return nil
}

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
