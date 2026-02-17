// Package modelfactory provides a registry and factory for creating ADK-compatible models.
package modelfactory

import (
	"fmt"

	"github.com/metalagman/norma/internal/adk/execmodel"
	"google.golang.org/adk/model"
)

// constructor is a function that creates a new model instance.
type constructor func(cfg ModelConfig) (model.LLM, error)

var constructors = map[string]constructor{
	ModelTypeGeminiAIStudio: NewGeminiAIStudio,
	ModelTypeOpenAI:         NewOpenAI,
	ModelTypeExec: func(cfg ModelConfig) (model.LLM, error) {
		return execmodel.New(execmodel.Config{
			Cmd:    cfg.Cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeGemini: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"gemini"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, "--approval-mode", "yolo")
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeClaude: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"claude"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeCodex: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"codex", "exec"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, "--sandbox", "workspace-write")
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeOpenCode: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"opencode", "run"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
}

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
	_, ok := constructors[modelType]
	return ok
}

// CreateModel creates an LLM instance by name.
// It returns an error if the model is not found or its type is unsupported.
func (f *Factory) CreateModel(name string) (model.LLM, error) {
	cfg, ok := f.registry[name]
	if !ok {
		return nil, fmt.Errorf("model %q not found or unsupported", name)
	}

	create, ok := constructors[cfg.Type]
	if !ok {
		// Should not happen if NewFactory filters supported types correctly.
		return nil, fmt.Errorf("unsupported model type %q for model %q", cfg.Type, name)
	}

	m, err := create(cfg)
	if err != nil {
		return nil, fmt.Errorf("create model %q: %w", name, err)
	}

	// Some models (like execmodel) can have their name overridden by the config key.
	if nm, ok := m.(interface{ SetName(string) }); ok {
		nm.SetName(name)
	}

	return m, nil
}
