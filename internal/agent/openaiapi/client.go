package openaiapi

import (
	"context"
	"fmt"
	"net/http"
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
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai model is required")
	}

	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai api key is required (set api_key)")
	}

	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithRequestTimeout(cfg.Timeout),
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	return &Client{
		cfg: Config{
			Model:   cfg.Model,
			BaseURL: cfg.BaseURL,
			Timeout: cfg.Timeout,
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
