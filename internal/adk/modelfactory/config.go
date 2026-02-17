package modelfactory

// ModelConfig describes how to run a model.
type ModelConfig struct {
	Type          string        `json:"type"                     mapstructure:"type"`
	Cmd           []string      `json:"cmd,omitempty"            mapstructure:"cmd"`
	Model         string        `json:"model,omitempty"          mapstructure:"model"`
	BaseURL       string        `json:"base_url,omitempty"       mapstructure:"base_url"`
	APIKey        string        `json:"api_key,omitempty"        mapstructure:"api_key"`
	Timeout       int           `json:"timeout,omitempty"        mapstructure:"timeout"`
	Path          string        `json:"path,omitempty"           mapstructure:"path"`
	UseTTY        *bool         `json:"use_tty,omitempty"        mapstructure:"use_tty"`
	MaxIterations int           `json:"max_iterations,omitempty" mapstructure:"max_iterations"`
	SubAgents     []ModelConfig `json:"sub_agents,omitempty"     mapstructure:"sub_agents"`
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
