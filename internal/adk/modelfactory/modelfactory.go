package modelfactory

import (
	"fmt"

	"github.com/metalagman/norma/internal/adk/execmodel"
	"google.golang.org/adk/model"
)

// Factory is a registry of models and their configurations.
type Factory struct {
	registry FactoryConfig
}

// NewFactory creates a new Factory from a map of model configurations.
// It only registers supported model types.
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

func isSupported(modelType string) bool {
	switch modelType {
	case ModelTypeGeminiAIStudio, ModelTypeOpenAI, ModelTypeExec:
		return true
	default:
		return false
	}
}

// CreateModel creates an LLM instance by name.
// It returns an error if the model is not found or its type is unsupported.
func (f *Factory) CreateModel(name string) (model.LLM, error) {
	cfg, ok := f.registry[name]
	if !ok {
		return nil, fmt.Errorf("model %q not found or unsupported", name)
	}

	switch cfg.Type {
	case ModelTypeGeminiAIStudio:
		return NewGeminiAIStudio(cfg)
	case ModelTypeOpenAI:
		return NewOpenAI(cfg)
	case ModelTypeExec:
		return execmodel.New(execmodel.Config{
			Name:   name,
			Cmd:    cfg.Cmd,
			UseTTY: cfg.UseTTY,
		})
	default:
		return nil, fmt.Errorf("unsupported model type %q", cfg.Type)
	}
}
