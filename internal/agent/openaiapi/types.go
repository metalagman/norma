package openaiapi

import "time"

const (
	defaultBaseURL   = "https://api.openai.com/v1"
	defaultAPIKeyEnv = "OPENAI_API_KEY"
	defaultTimeout   = 60 * time.Second
)

// Config is OpenAI API client configuration.
type Config struct {
	Model     string
	BaseURL   string
	APIKey    string
	APIKeyEnv string
	Timeout   time.Duration
}

// CompletionRequest is a single OpenAI responses API request.
type CompletionRequest struct {
	Instructions string
	Input        string
}

// CompletionResponse is a single OpenAI responses API response.
type CompletionResponse struct {
	OutputText string
}
