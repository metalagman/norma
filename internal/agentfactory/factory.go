package agentfactory

import (
	"fmt"

	"github.com/metalagman/norma/internal/agentfactory/execmodel"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
)

// Factory is a registry of agents and their configurations.
type Factory struct {
	registry FactoryConfig
}

// NewFactory creates a new Factory from a map of agent configurations.
// It only registers supported agent types.
func NewFactory(config FactoryConfig) *Factory {
	f := &Factory{
		registry: make(FactoryConfig),
	}
	for name, cfg := range config {
		if isSupported(cfg.Type) {
			f.registry[name] = cfg
		}
	}
	return f
}

func isSupported(agentType string) bool {
	switch agentType {
	case AgentTypeGeminiAIStudio, AgentTypeOpenAI, AgentTypeExec:
		return true
	default:
		return false
	}
}

// CreateModel creates an LLM instance by name.
// It returns an error if the agent is not found or its type is unsupported.
func (f *Factory) CreateModel(name string) (model.LLM, error) {
	cfg, ok := f.registry[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found or unsupported", name)
	}

	switch cfg.Type {
	case AgentTypeGeminiAIStudio:
		return NewGeminiAIStudioLLM(cfg)
	case AgentTypeOpenAI:
		return NewOpenAILLM(cfg)
	case AgentTypeExec:
		return execmodel.New(execmodel.Config{
			Name:   name,
			Cmd:    cfg.Cmd,
			UseTTY: cfg.UseTTY,
		})
	default:
		return nil, fmt.Errorf("unsupported agent type %q", cfg.Type)
	}
}

// CreateAgent creates an agent instance by name.
// It returns an error if the agent is not found or its type is unsupported.
func (f *Factory) CreateAgent(name string, agentName, description, instruction string) (agent.Agent, error) {
	m, err := f.CreateModel(name)
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name:        agentName,
		Description: description,
		Model:       m,
		Instruction: instruction,
	})
}
