package modelfactory

// ModelConfig describes how to run a model.
type ModelConfig struct {
	Type string `json:"type"           mapstructure:"type"`

	// Gemini and OpenAI fields
	Model   string `json:"model,omitempty"    mapstructure:"model"`
	APIKey  string `json:"api_key,omitempty"   mapstructure:"api_key"`
	BaseURL string `json:"base_url,omitempty"  mapstructure:"base_url"`

	// Exec fields
	Cmd    []string `json:"cmd,omitempty"     mapstructure:"cmd"`
	UseTTY bool     `json:"use_tty,omitempty" mapstructure:"use_tty"`

	Timeout int `json:"timeout,omitempty" mapstructure:"timeout"`
}

// FactoryConfig is a map of model configurations.
type FactoryConfig map[string]ModelConfig

const (
	// ModelTypeGeminiAIStudio is the type for Gemini AI Studio models.
	ModelTypeGeminiAIStudio = "gemini_aistudio"
	// ModelTypeOpenAI is the type for OpenAI models.
	ModelTypeOpenAI = "openai"
	// ModelTypeExec is the type for executive models.
	ModelTypeExec = "exec"
)
