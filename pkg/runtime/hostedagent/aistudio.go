package hostedagent

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"
)

// NewAIStudioModel creates an ADK-compatible Gemini model backed by Google AI
// Studio.
func NewAIStudioModel(ctx context.Context, apiKey, modelName string) (model.LLM, error) {
	if strings.TrimSpace(modelName) == "" {
		return nil, fmt.Errorf("model is required for aistudio provider")
	}

	var cfg *genai.ClientConfig
	if strings.TrimSpace(apiKey) != "" {
		cfg = &genai.ClientConfig{APIKey: apiKey}
	}

	llmModel, err := gemini.NewModel(ctx, modelName, cfg)
	if err != nil {
		return nil, fmt.Errorf("create gemini model: %w", err)
	}

	return llmModel, nil
}
