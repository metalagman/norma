package openaiapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

// Client wraps OpenAI responses API for oneshot calls.
type Client struct {
	cfg    Config
	client openai.Client
}

// NewClient constructs a new OpenAI API client.
func NewClient(cfg Config, httpClient *http.Client) (*Client, error) {
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("openai model is required")
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		envKey := strings.TrimSpace(cfg.APIKeyEnv)
		if envKey == "" {
			envKey = defaultAPIKeyEnv
		}
		apiKey = strings.TrimSpace(os.Getenv(envKey))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("openai api key is required (set api_key or api_key_env)")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		option.WithRequestTimeout(timeout),
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	return &Client{
		cfg: Config{
			Model:   model,
			BaseURL: baseURL,
			Timeout: timeout,
		},
		client: openai.NewClient(opts...),
	}, nil
}

// Complete executes a single Responses API request.
func (c *Client) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	resp, err := c.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        c.cfg.Model,
		Instructions: openai.String(req.Instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(req.Input),
		},
	})
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("openai responses.create: %w", err)
	}
	if msg := strings.TrimSpace(resp.Error.Message); msg != "" {
		return CompletionResponse{}, fmt.Errorf("openai response failed: %s", msg)
	}

	output := strings.TrimSpace(resp.OutputText())
	if output == "" {
		return CompletionResponse{}, fmt.Errorf("openai response did not contain output text")
	}

	return CompletionResponse{OutputText: output}, nil
}
