package playgroundcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"
)

type codexMCPServerOptions struct {
	CodexBin  string
	CodexArgs []string
	JSON      bool
}

type codexMCPMethodSupport struct {
	Tools             bool `json:"tools"`
	Prompts           bool `json:"prompts"`
	Resources         bool `json:"resources"`
	ResourceTemplates bool `json:"resource_templates"`
}

type codexADKToolSummary struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	IsLongRunning bool   `json:"is_long_running"`
}

type codexMCPInspectOutput struct {
	Command           []string                `json:"command"`
	Initialize        *mcp.InitializeResult   `json:"initialize,omitempty"`
	Support           codexMCPMethodSupport   `json:"support"`
	Tools             []*mcp.Tool             `json:"tools,omitempty"`
	Prompts           []*mcp.Prompt           `json:"prompts,omitempty"`
	Resources         []*mcp.Resource         `json:"resources,omitempty"`
	ResourceTemplates []*mcp.ResourceTemplate `json:"resource_templates,omitempty"`
	ADKTools          []codexADKToolSummary   `json:"adk_tools,omitempty"`
}

func codexMCPServerCommand() *cobra.Command {
	opts := codexMCPServerOptions{CodexBin: "codex"}
	return newACPPlaygroundCommand(
		"codex-mcp-server",
		"Inspect Codex MCP server capabilities and tools",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.CodexBin, "codex-bin", opts.CodexBin, "Codex executable path")
			cmd.Flags().StringArrayVar(&opts.CodexArgs, "codex-arg", nil, "extra Codex MCP server argument (repeatable)")
			cmd.Flags().BoolVar(&opts.JSON, "json", false, "print output as JSON")
		},
		func(ctx context.Context, repoRoot string, _ io.Reader, stdout, stderr io.Writer) error {
			return runCodexMCPServer(ctx, repoRoot, opts, stdout, stderr)
		},
	)
}

func runCodexMCPServer(ctx context.Context, repoRoot string, opts codexMCPServerOptions, stdout, stderr io.Writer) error {
	lockedStderr := &syncWriter{writer: stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Str("component", "playground.codex_mcp_server").Logger()

	command := buildCodexMCPServerCommand(opts)
	logger.Info().Str("repo_root", repoRoot).Strs("command", command).Msg("starting Codex MCP server playground")

	inspectOutput, err := inspectCodexMCPServer(ctx, repoRoot, command, lockedStderr, logger)
	if err != nil {
		logger.Error().Err(err).Msg("failed to inspect Codex MCP server")
		return err
	}

	if opts.JSON {
		if err := writeCodexMCPInspectJSON(stdout, inspectOutput); err != nil {
			logger.Error().Err(err).Msg("failed to write JSON output")
			return err
		}
		return nil
	}

	if err := writeCodexMCPInspectHuman(stdout, inspectOutput); err != nil {
		logger.Error().Err(err).Msg("failed to write human output")
		return err
	}
	return nil
}

func buildCodexMCPServerCommand(opts codexMCPServerOptions) []string {
	command := make([]string, 0, 2+len(opts.CodexArgs))
	command = append(command, opts.CodexBin, "mcp-server")
	command = append(command, opts.CodexArgs...)
	return command
}

func inspectCodexMCPServer(ctx context.Context, repoRoot string, command []string, stderr io.Writer, logger zerolog.Logger) (*codexMCPInspectOutput, error) {
	inspectOutput, err := inspectCodexMCPServerRaw(ctx, repoRoot, command, stderr, logger)
	if err != nil {
		return nil, err
	}

	adkTools, err := inspectCodexMCPServerADK(ctx, repoRoot, command, stderr, logger)
	if err != nil {
		return nil, err
	}
	inspectOutput.ADKTools = adkTools
	return inspectOutput, nil
}

func inspectCodexMCPServerRaw(ctx context.Context, repoRoot string, command []string, stderr io.Writer, logger zerolog.Logger) (*codexMCPInspectOutput, error) {
	transport, err := newCodexMCPCommandTransport(ctx, repoRoot, command, stderr)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "norma-playground-codex-mcp-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to codex mcp server: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			logger.Warn().Err(closeErr).Msg("failed to close Codex MCP session")
		}
	}()

	inspectOutput := &codexMCPInspectOutput{
		Command: append([]string(nil), command...),
	}
	inspectOutput.Initialize = session.InitializeResult()
	inspectOutput.Support = extractSupportFromInitialize(inspectOutput.Initialize)

	if inspectOutput.Support.Tools {
		inspectOutput.Tools, inspectOutput.Support.Tools, err = collectMCPTools(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
	}
	if inspectOutput.Support.Prompts {
		inspectOutput.Prompts, inspectOutput.Support.Prompts, err = collectMCPPrompts(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list prompts: %w", err)
		}
	}
	if inspectOutput.Support.Resources {
		inspectOutput.Resources, inspectOutput.Support.Resources, err = collectMCPResources(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list resources: %w", err)
		}
	}
	if inspectOutput.Support.ResourceTemplates {
		inspectOutput.ResourceTemplates, inspectOutput.Support.ResourceTemplates, err = collectMCPResourceTemplates(ctx, session)
		if err != nil {
			return nil, fmt.Errorf("list resource templates: %w", err)
		}
	}
	return inspectOutput, nil
}

func inspectCodexMCPServerADK(ctx context.Context, repoRoot string, command []string, stderr io.Writer, logger zerolog.Logger) ([]codexADKToolSummary, error) {
	transport, err := newCodexMCPCommandTransport(ctx, repoRoot, command, stderr)
	if err != nil {
		return nil, err
	}
	toolset, err := mcptoolset.New(mcptoolset.Config{Transport: transport})
	if err != nil {
		return nil, fmt.Errorf("create adk mcp toolset: %w", err)
	}
	tools, err := toolset.Tools(playgroundReadonlyContext{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("load tools via adk mcp toolset: %w", err)
	}

	toolSummaries := make([]codexADKToolSummary, 0, len(tools))
	for _, t := range tools {
		toolSummaries = append(toolSummaries, codexADKToolSummary{
			Name:          t.Name(),
			Description:   t.Description(),
			IsLongRunning: t.IsLongRunning(),
		})
	}
	logger.Info().Int("tool_count", len(toolSummaries)).Msg("loaded MCP tools via ADK toolset")
	return toolSummaries, nil
}

func newCodexMCPCommandTransport(ctx context.Context, repoRoot string, command []string, stderr io.Writer) (*mcp.CommandTransport, error) {
	if len(command) == 0 {
		return nil, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = repoRoot
	cmd.Stderr = stderr
	return &mcp.CommandTransport{Command: cmd}, nil
}

func collectMCPTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, bool, error) {
	tools := make([]*mcp.Tool, 0)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			if isMCPMethodUnsupported(err) {
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

func collectMCPPrompts(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Prompt, bool, error) {
	prompts := make([]*mcp.Prompt, 0)
	for prompt, err := range session.Prompts(ctx, nil) {
		if err != nil {
			if isMCPMethodUnsupported(err) {
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

func collectMCPResources(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Resource, bool, error) {
	resources := make([]*mcp.Resource, 0)
	for resource, err := range session.Resources(ctx, nil) {
		if err != nil {
			if isMCPMethodUnsupported(err) {
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

func collectMCPResourceTemplates(ctx context.Context, session *mcp.ClientSession) ([]*mcp.ResourceTemplate, bool, error) {
	resourceTemplates := make([]*mcp.ResourceTemplate, 0)
	for resourceTemplate, err := range session.ResourceTemplates(ctx, nil) {
		if err != nil {
			if isMCPMethodUnsupported(err) {
				return nil, false, nil
			}
			return nil, true, err
		}
		if resourceTemplate != nil {
			resourceTemplates = append(resourceTemplates, resourceTemplate)
		}
	}
	return resourceTemplates, true, nil
}

func writeCodexMCPInspectJSON(stdout io.Writer, inspectOutput *codexMCPInspectOutput) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(inspectOutput)
}

func writeCodexMCPInspectHuman(stdout io.Writer, inspectOutput *codexMCPInspectOutput) error {
	if inspectOutput.Initialize == nil {
		if _, err := fmt.Fprintln(stdout, "Connected to MCP server, but initialize result is empty."); err != nil {
			return err
		}
	} else {
		serverName := ""
		serverVersion := ""
		if inspectOutput.Initialize.ServerInfo != nil {
			serverName = inspectOutput.Initialize.ServerInfo.Name
			serverVersion = inspectOutput.Initialize.ServerInfo.Version
		}
		if _, err := fmt.Fprintf(stdout, "Server: %s %s\n", strings.TrimSpace(serverName), strings.TrimSpace(serverVersion)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "Protocol: %s\n", inspectOutput.Initialize.ProtocolVersion); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "Capabilities: %s\n", formatServerCapabilities(inspectOutput.Initialize.Capabilities)); err != nil {
			return err
		}
		if strings.TrimSpace(inspectOutput.Initialize.Instructions) != "" {
			if _, err := fmt.Fprintf(stdout, "Instructions: %s\n", inspectOutput.Initialize.Instructions); err != nil {
				return err
			}
		}
	}

	if err := writeToolSummary(stdout, inspectOutput.Support.Tools, inspectOutput.Tools); err != nil {
		return err
	}
	if err := writePromptSummary(stdout, inspectOutput.Support.Prompts, inspectOutput.Prompts); err != nil {
		return err
	}
	if err := writeResourceSummary(stdout, inspectOutput.Support.Resources, inspectOutput.Resources); err != nil {
		return err
	}
	if err := writeResourceTemplateSummary(stdout, inspectOutput.Support.ResourceTemplates, inspectOutput.ResourceTemplates); err != nil {
		return err
	}
	if err := writeADKToolSummary(stdout, inspectOutput.ADKTools); err != nil {
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
		items = append(items, summaryItem{name: tool.Name, description: tool.Description})
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

func writeResourceTemplateSummary(stdout io.Writer, supported bool, resourceTemplates []*mcp.ResourceTemplate) error {
	items := make([]summaryItem, 0, len(resourceTemplates))
	for _, resourceTemplate := range resourceTemplates {
		if resourceTemplate == nil {
			continue
		}
		items = append(items, summaryItem{name: resourceTemplate.URITemplate, description: resourceTemplate.Description})
	}
	return writeSummaryList(stdout, "Resource templates", supported, items)
}

func writeADKToolSummary(stdout io.Writer, tools []codexADKToolSummary) error {
	items := make([]summaryItem, 0, len(tools))
	for _, tool := range tools {
		items = append(items, summaryItem{name: tool.Name, description: tool.Description})
	}
	return writeSummaryList(stdout, "ADK tools", true, items)
}

func isMCPMethodUnsupported(err error) bool {
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
	parts := make([]string, 0, 4)
	parts = append(parts, fmt.Sprintf("tools=%t", capabilities.Tools != nil))
	parts = append(parts, fmt.Sprintf("prompts=%t", capabilities.Prompts != nil))
	parts = append(parts, fmt.Sprintf("resources=%t", capabilities.Resources != nil))
	parts = append(parts, fmt.Sprintf("logging=%t", capabilities.Logging != nil))
	if len(capabilities.Experimental) > 0 {
		parts = append(parts, "experimental=true")
	}
	return strings.Join(parts, " ")
}

func extractSupportFromInitialize(initialize *mcp.InitializeResult) codexMCPMethodSupport {
	if initialize == nil || initialize.Capabilities == nil {
		return codexMCPMethodSupport{
			Tools:             true,
			Prompts:           true,
			Resources:         true,
			ResourceTemplates: true,
		}
	}
	return codexMCPMethodSupport{
		Tools:             initialize.Capabilities.Tools != nil,
		Prompts:           initialize.Capabilities.Prompts != nil,
		Resources:         initialize.Capabilities.Resources != nil,
		ResourceTemplates: initialize.Capabilities.Resources != nil,
	}
}

type summaryItem struct {
	name        string
	description string
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
	}
	return nil
}

type playgroundReadonlyContext struct {
	context.Context
}

var _ agent.ReadonlyContext = (*playgroundReadonlyContext)(nil)

func (c playgroundReadonlyContext) UserContent() *genai.Content {
	return nil
}

func (c playgroundReadonlyContext) InvocationID() string {
	return "norma-playground-codex-mcp-server"
}

func (c playgroundReadonlyContext) AgentName() string {
	return "playground.codex_mcp_server"
}

func (c playgroundReadonlyContext) ReadonlyState() session.ReadonlyState {
	return nil
}

func (c playgroundReadonlyContext) UserID() string {
	return "norma-playground-user"
}

func (c playgroundReadonlyContext) AppName() string {
	return "norma-playground"
}

func (c playgroundReadonlyContext) SessionID() string {
	return "playground-codex-mcp-server"
}

func (c playgroundReadonlyContext) Branch() string {
	return ""
}
