package agentconfig

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

// Config describes how to run an agent.
type Config struct {
	Type      string   `json:"type"                 mapstructure:"type"       validate:"required,oneof=generic_acp codex_acp opencode_acp gemini_acp"`
	Cmd       []string `json:"cmd,omitempty"        mapstructure:"cmd"`
	ExtraArgs []string `json:"extra_args,omitempty" mapstructure:"extra_args"`
	Model     string   `json:"model,omitempty"      mapstructure:"model"      validate:"omitempty,min=1"`
	BaseURL   string   `json:"base_url,omitempty"   mapstructure:"base_url"   validate:"omitempty,min=1"`
	APIKey    string   `json:"api_key,omitempty"    mapstructure:"api_key"    validate:"omitempty,min=1"`
	Timeout   int      `json:"timeout,omitempty"    mapstructure:"timeout"    validate:"omitempty,min=1"`
	UseTTY    *bool    `json:"use_tty,omitempty"    mapstructure:"use_tty"`
}

var configValidator = newConfigValidator()

func newConfigValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return fld.Name
		}
		return name
	})
	return v
}

// Validate validates the agent configuration.
func (c Config) Validate() error {
	errs := make([]string, 0)

	if err := configValidator.Struct(c); err != nil {
		if invErr, ok := err.(*validator.InvalidValidationError); ok {
			return fmt.Errorf("validate agent config: %w", invErr)
		}
		for _, validationErr := range err.(validator.ValidationErrors) {
			errs = append(errs, formatValidationError(validationErr))
		}
	}

	switch c.Type {
	case AgentTypeGenericACP:
		if len(c.Cmd) == 0 {
			errs = append(errs, fmt.Sprintf("cmd is required for type %s", c.Type))
		}
	case AgentTypeCodexACP, AgentTypeOpenCodeACP, AgentTypeGeminiACP:
		if len(c.Cmd) > 0 {
			errs = append(errs, fmt.Sprintf("cmd must be omitted for type %s", c.Type))
		}
	}

	for i, arg := range c.Cmd {
		if arg == "" {
			errs = append(errs, fmt.Sprintf("cmd[%d] must have at least 1 character", i))
		}
	}
	for i, arg := range c.ExtraArgs {
		if arg == "" {
			errs = append(errs, fmt.Sprintf("extra_args[%d] must have at least 1 character", i))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)

	return fmt.Errorf("agent config schema validation failed: %s", strings.Join(errs, "; "))
}

func formatValidationError(err validator.FieldError) string {
	field := err.Field()
	switch err.Tag() {
	case "required":
		return field + " is required"
	case "oneof":
		return field + " must be one of: " + err.Param()
	case "min":
		return field + " must be at least " + err.Param()
	default:
		return field + " failed validation rule " + err.Tag()
	}
}

const (
	// AgentTypeGenericACP is the type for custom ACP CLI executables.
	AgentTypeGenericACP = "generic_acp"

	// AgentTypeGeminiACP is the alias for Gemini CLI ACP mode.
	AgentTypeGeminiACP = "gemini_acp"
	// AgentTypeCodexACP is the alias for Codex ACP bridge mode.
	AgentTypeCodexACP = "codex_acp"
	// AgentTypeOpenCodeACP is the alias for OpenCode CLI ACP mode.
	AgentTypeOpenCodeACP = "opencode_acp"
)

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeGenericACP, AgentTypeGeminiACP, AgentTypeOpenCodeACP, AgentTypeCodexACP:
		return true
	default:
		return false
	}
}

// IsPlannerSupportedType reports whether planner mode supports the agent type.
func IsPlannerSupportedType(agentType string) bool {
	return IsACPType(agentType)
}

// NormalizeACPConfig canonicalizes ACP aliases to generic_acp while preserving behavior.
func NormalizeACPConfig(cfg Config, executablePath string) (Config, error) {
	normalized := cfg

	switch strings.TrimSpace(cfg.Type) {
	case AgentTypeGeminiACP:
		normalized.Type = AgentTypeGenericACP
		normalized.Cmd = []string{"gemini", "--experimental-acp"}
		if cfg.Model != "" {
			normalized.Cmd = append(normalized.Cmd, "--model", cfg.Model)
		}
	case AgentTypeOpenCodeACP:
		normalized.Type = AgentTypeGenericACP
		normalized.Cmd = []string{"opencode", "acp"}
	case AgentTypeCodexACP:
		exePath := strings.TrimSpace(executablePath)
		if exePath == "" {
			return Config{}, fmt.Errorf("resolve executable path: empty")
		}
		normalized.Type = AgentTypeGenericACP
		normalized.Cmd = []string{exePath, "tool", "codex-acp"}
		if cfg.Model != "" {
			normalized.Cmd = append(normalized.Cmd, "--codex-model", cfg.Model)
		}
	}

	return normalized, nil
}

// NormalizeACPConfigs canonicalizes ACP aliases for a map of named agent configs.
func NormalizeACPConfigs(cfgs map[string]Config, executablePath string) (map[string]Config, error) {
	if len(cfgs) == 0 {
		return cfgs, nil
	}

	normalized := make(map[string]Config, len(cfgs))
	for name, cfg := range cfgs {
		normCfg, err := NormalizeACPConfig(cfg, executablePath)
		if err != nil {
			return nil, fmt.Errorf("normalize agent %q: %w", name, err)
		}
		normalized[name] = normCfg
	}

	return normalized, nil
}
