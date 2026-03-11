// Package agentfactory provides a registry and factory for creating ADK-compatible agents.
package agentfactory

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/acpagent"
	"github.com/metalagman/norma/internal/adk/agentconfig"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
)

// CreationRequest defines the parameters for creating a new agent.
type CreationRequest struct {
	Name              string
	Description       string
	SystemInstruction string
	WorkingDirectory  string
	Stdout            io.Writer
	Stderr            io.Writer
	PermissionHandler func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	Tools             []tool.Tool
}

// constructor is a function that creates a new agent instance.
type constructor func(ctx context.Context, cfg agentconfig.Config, req CreationRequest) (agent.Agent, error)

// Factory is a registry of agent configurations.
type Factory struct {
	registry map[string]agentconfig.Config
}

// NewFactory creates a new Factory from a map of agent configurations.
func NewFactory(agents map[string]agentconfig.Config) *Factory {
	return &Factory{
		registry: agents,
	}
}

// CreateAgent creates an agent.Agent instance by name and creation request.
// It returns an error if the agent is not found or its type is unsupported.
func (f *Factory) CreateAgent(ctx context.Context, name string, req CreationRequest) (agent.Agent, error) {
	if strings.TrimSpace(req.WorkingDirectory) == "" {
		return nil, fmt.Errorf("working directory is required")
	}

	cfg, ok := f.registry[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found or unsupported", name)
	}

	create, ok := constructors[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported agent type %q for agent %q", cfg.Type, name)
	}

	ag, err := create(ctx, cfg, req)
	if err != nil {
		return nil, fmt.Errorf("create agent %q: %w", name, err)
	}

	return ag, nil
}

// constructors registry.
var constructors = map[string]constructor{
	agentconfig.AgentTypeGenericACP: acpConstructor,
}

var acpConstructor = func(ctx context.Context, cfg agentconfig.Config, req CreationRequest) (agent.Agent, error) {
	cmd, err := ResolveACPCommand(cfg)
	if err != nil {
		return nil, err
	}

	return acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              req.Name,
		Description:       req.Description,
		Model:             cfg.Model,
		SystemPrompt:      req.SystemInstruction,
		Command:           cmd,
		WorkingDir:        req.WorkingDirectory,
		Stderr:            req.Stderr,
		PermissionHandler: req.PermissionHandler,
		Tools:             req.Tools,
	})
}

// ResolveACPCommand resolves the command for ACP-backed agent types.
func ResolveACPCommand(cfg agentconfig.Config) ([]string, error) {
	if cfg.Type != agentconfig.AgentTypeGenericACP {
		return nil, fmt.Errorf("unknown acp agent type %q", cfg.Type)
	}

	if len(cfg.Cmd) == 0 {
		return nil, fmt.Errorf("generic_acp agent requires cmd")
	}
	cmd := cfg.Cmd

	res := resolveTemplatedCmd(cmd, cfg.Model)
	if len(cfg.ExtraArgs) > 0 {
		res = append(res, resolveTemplatedCmd(cfg.ExtraArgs, cfg.Model)...)
	}
	return res, nil
}

func resolveTemplatedCmd(cmd []string, model string) []string {
	if len(cmd) == 0 {
		return nil
	}
	res := make([]string, len(cmd))
	for i, arg := range cmd {
		res[i] = strings.ReplaceAll(arg, "{{.Model}}", model)
	}
	return res
}
