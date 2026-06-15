package hostedagent

import (
	"fmt"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// Config describes an ADK agent backed by a hosted model implementation.
type Config struct {
	// Name is the ADK agent display name.
	Name string
	// Description is the ADK agent description.
	Description string
	// Instruction is appended after GlobalInstruction for each model request.
	Instruction string
	// GlobalInstruction is prepended before Instruction for each model request.
	GlobalInstruction string
	// Model handles underlying content generation.
	Model model.LLM
	// Tools contains ADK-native tools available to the hosted agent.
	Tools []tool.Tool
	// Toolsets contains ADK-native toolsets available to the hosted agent.
	Toolsets []tool.Toolset
}

// New wraps a hosted model as an ADK agent.
func New(cfg Config) (adkagent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("model is required")
	}

	agent, err := llmagent.New(llmagent.Config{
		Name:                     cfg.Name,
		Description:              cfg.Description,
		Model:                    cfg.Model,
		Instruction:              strings.TrimSpace(cfg.Instruction),
		GlobalInstruction:        strings.TrimSpace(cfg.GlobalInstruction),
		Tools:                    append([]tool.Tool(nil), cfg.Tools...),
		Toolsets:                 append([]tool.Toolset(nil), cfg.Toolsets...),
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("create hosted agent: %w", err)
	}

	return agent, nil
}
