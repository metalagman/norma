package agentconfig

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

//go:embed schema.json
var schemaJSON string

// Config describes how to run an agent.
type Config struct {
	Type      string   `json:"type"                 mapstructure:"type"`
	Cmd       []string `json:"cmd,omitempty"        mapstructure:"cmd"`
	ExtraArgs []string `json:"extra_args,omitempty" mapstructure:"extra_args"`
	Model     string   `json:"model,omitempty"      mapstructure:"model"`
	BaseURL   string   `json:"base_url,omitempty"   mapstructure:"base_url"`
	APIKey    string   `json:"api_key,omitempty"    mapstructure:"api_key"`
	Timeout   int      `json:"timeout,omitempty"    mapstructure:"timeout"`
	UseTTY    *bool    `json:"use_tty,omitempty"    mapstructure:"use_tty"`
}

// Validate validates the agent configuration against the JSON schema.
func (c Config) Validate() error {
	schemaLoader := gojsonschema.NewStringLoader(schemaJSON)
	documentLoader := gojsonschema.NewGoLoader(c)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("validate agent config schema: %w", err)
	}
	if result.Valid() {
		return nil
	}

	errs := make([]string, 0, len(result.Errors()))
	for _, schemaErr := range result.Errors() {
		errs = append(errs, schemaErr.String())
	}
	sort.Strings(errs)

	return fmt.Errorf("agent config schema validation failed: %s", strings.Join(errs, "; "))
}

const (
	// AgentTypeGeminiAIStudio is the type for Gemini AI Studio models.
	AgentTypeGeminiAIStudio = "gemini_aistudio"
	// AgentTypeExec is the type for executive models.
	AgentTypeExec = "exec"
	// AgentTypeACPExec is the type for custom ACP CLI executables.
	AgentTypeACPExec = "acp_exec"

	// AgentTypeGemini is the alias for gemini CLI.
	AgentTypeGemini = "gemini"
	// AgentTypeGeminiACP is the alias for Gemini CLI ACP mode.
	AgentTypeGeminiACP = "gemini_acp"
	// AgentTypeClaude is the alias for claude CLI.
	AgentTypeClaude = "claude"
	// AgentTypeCodex is the alias for codex CLI.
	AgentTypeCodex = "codex"
	// AgentTypeCodexACP is the alias for Codex ACP bridge mode.
	AgentTypeCodexACP = "codex_acp"
	// AgentTypeOpenCode is the alias for opencode CLI.
	AgentTypeOpenCode = "opencode"
	// AgentTypeOpenCodeACP is the alias for OpenCode CLI ACP mode.
	AgentTypeOpenCodeACP = "opencode_acp"
)

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeACPExec, AgentTypeGeminiACP, AgentTypeOpenCodeACP, AgentTypeCodexACP:
		return true
	default:
		return false
	}
}

// HasSetModelSupport reports whether an agent type supports session/set_model.
func HasSetModelSupport(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeOpenCodeACP:
		return true
	case AgentTypeCodexACP, AgentTypeGeminiACP, AgentTypeACPExec:
		return false
	default:
		return false
	}
}

// IsLLMType reports whether an agent type uses a direct LLM model runtime.
func IsLLMType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeCodex, AgentTypeOpenCode, AgentTypeGemini, AgentTypeClaude, AgentTypeGeminiAIStudio:
		return true
	default:
		return false
	}
}

// IsPlannerSupportedType reports whether planner mode supports the agent type.
func IsPlannerSupportedType(agentType string) bool {
	return IsLLMType(agentType) || IsACPType(agentType)
}
