package openaiapi

import "time"

const (
	defaultBaseURL   = "https://api.openai.com/v1"
	defaultTimeout   = 60 * time.Second
)

// Config is OpenAI API client configuration.
type Config struct {
	Model     string
	BaseURL   string
	APIKey    string
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
