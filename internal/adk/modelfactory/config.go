package modelfactory

// ModelConfig describes how to run a model.
type ModelConfig struct {
	Type      string   `json:"type"                 mapstructure:"type"`
	Cmd       []string `json:"cmd,omitempty"        mapstructure:"cmd"`
	ExtraArgs []string `json:"extra_args,omitempty" mapstructure:"extra_args"`
	Model     string   `json:"model,omitempty"      mapstructure:"model"`
	BaseURL   string   `json:"base_url,omitempty"   mapstructure:"base_url"`
	APIKey    string   `json:"api_key,omitempty"    mapstructure:"api_key"`
	Timeout   int      `json:"timeout,omitempty"    mapstructure:"timeout"`
	UseTTY    *bool    `json:"use_tty,omitempty"    mapstructure:"use_tty"`
}

// FactoryConfig is a map of model configurations.
type FactoryConfig map[string]ModelConfig

const (
	// ModelTypeGeminiAIStudio is the type for Gemini AI Studio models.
	ModelTypeGeminiAIStudio = "gemini_aistudio"
	// ModelTypeExec is the type for executive models.
	ModelTypeExec = "exec"
	// ModelTypeACPExec is the type for custom ACP CLI executables.
	ModelTypeACPExec = "acp_exec"

	// ModelTypeGemini is the alias for gemini CLI.
	ModelTypeGemini = "gemini"
	// ModelTypeGeminiACP is the alias for Gemini CLI ACP mode.
	ModelTypeGeminiACP = "gemini_acp"
	// ModelTypeClaude is the alias for claude CLI.
	ModelTypeClaude = "claude"
	// ModelTypeCodex is the alias for codex CLI.
	ModelTypeCodex = "codex"
	// ModelTypeCodexACP is the alias for Codex ACP bridge mode.
	ModelTypeCodexACP = "codex_acp"
	// ModelTypeOpenCode is the alias for opencode CLI.
	ModelTypeOpenCode = "opencode"
	// ModelTypeOpenCodeACP is the alias for OpenCode CLI ACP mode.
	ModelTypeOpenCodeACP = "opencode_acp"
)
