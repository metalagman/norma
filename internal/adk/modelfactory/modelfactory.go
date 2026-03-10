// Package modelfactory provides a registry and factory for creating ADK-compatible models.
package modelfactory

import (
	"fmt"
	"os"
	"strings"

	"github.com/metalagman/norma/internal/adk/acpmodel"
	"github.com/metalagman/norma/internal/adk/agentconfig"
	"github.com/metalagman/norma/internal/adk/execmodel"
	"google.golang.org/adk/model"
)

// constructor is a function that creates a new model instance.
type constructor func(cfg ModelConfig) (model.LLM, error)

var defaultAliasArgs = map[string][]string{
	ModelTypeGemini:   {"--approval-mode", "yolo"},
	ModelTypeCodex:    {"exec", "--sandbox", "workspace-write"},
	ModelTypeOpenCode: {"run"},
}

var constructors = map[string]constructor{
	ModelTypeGeminiAIStudio: NewGeminiAIStudio,
	ModelTypeExec: func(cfg ModelConfig) (model.LLM, error) {
		cmd := append([]string(nil), cfg.Cmd...)
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeGemini: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"gemini"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, defaultAliasArgs[ModelTypeGemini]...)
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
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
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeCodex: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"codex"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, defaultAliasArgs[ModelTypeCodex]...)
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},
	ModelTypeOpenCode: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"opencode"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, defaultAliasArgs[ModelTypeOpenCode]...)
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return execmodel.New(execmodel.Config{
			Cmd:    cmd,
			UseTTY: cfg.UseTTY != nil && *cfg.UseTTY,
		})
	},

	ModelTypeACPExec: func(cfg ModelConfig) (model.LLM, error) {
		cmd := append([]string(nil), cfg.Cmd...)
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return acpmodel.New(acpmodel.Config{
			Command:     cmd,
			Model:       cfg.Model,
			HasSetModel: agentconfig.HasSetModelSupport(cfg.Type),
		})
	},
	ModelTypeGeminiACP: func(cfg ModelConfig) (model.LLM, error) {
		cmd := []string{"gemini", "--experimental-acp"}
		if cfg.Model != "" {
			cmd = append(cmd, "--model", cfg.Model)
		}
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return acpmodel.New(acpmodel.Config{
			Command:     cmd,
			Model:       cfg.Model,
			HasSetModel: agentconfig.HasSetModelSupport(cfg.Type),
		})
	},
	ModelTypeOpenCodeACP: func(cfg ModelConfig) (model.LLM, error) {
		cmd := make([]string, 0, 2+len(cfg.ExtraArgs))
		cmd = append(cmd, "opencode", "acp")
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return acpmodel.New(acpmodel.Config{
			Command:     cmd,
			Model:       cfg.Model,
			HasSetModel: agentconfig.HasSetModelSupport(cfg.Type),
		})
	},
	ModelTypeCodexACP: func(cfg ModelConfig) (model.LLM, error) {
		exePath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
		cmd := []string{exePath, "proxy", "codex-acp"}
		if cfg.Model != "" {
			cmd = append(cmd, "--codex-model", cfg.Model)
		}
		cmd = append(cmd, resolveTemplatedArgs(cfg.ExtraArgs, cfg.Model)...)
		return acpmodel.New(acpmodel.Config{
			Command:     cmd,
			Model:       cfg.Model,
			HasSetModel: agentconfig.HasSetModelSupport(cfg.Type),
		})
	},
}

func resolveTemplatedArgs(args []string, model string) []string {
	if len(args) == 0 {
		return nil
	}
	res := make([]string, len(args))
	for i, arg := range args {
		res[i] = strings.ReplaceAll(arg, "{{.Model}}", model)
	}
	return res
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
	if nm, ok := m.(interface{ SetName(name string) }); ok {
		nm.SetName(name)
	}

	return m, nil
}
