package modelfactory

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var _ model.LLM = (*openAI)(nil)

// openAI implements model.LLM using the OpenAI Responses API.
type openAI struct {
	client *openai.Client
	model  string
}

// Name returns the model name.
func (m *openAI) Name() string {
	return "openai"
}

// GenerateContent executes a request against the OpenAI Responses API.
func (m *openAI) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stream {
			yield(nil, fmt.Errorf("streaming is not supported by OpenAI Responses API"))
			return
		}

		var instructions []string
		var inputs []string

		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, part := range req.Config.SystemInstruction.Parts {
				if part.Text != "" {
					instructions = append(instructions, part.Text)
				}
			}
		}

		for _, content := range req.Contents {
			if content.Role == "system" {
				for _, part := range content.Parts {
					if part.Text != "" {
						instructions = append(instructions, part.Text)
					}
				}
			} else {
				for _, part := range content.Parts {
					if part.Text != "" {
						inputs = append(inputs, part.Text)
					}
				}
			}
		}

		resp, err := m.client.Responses.New(ctx, responses.ResponseNewParams{
			Model:        m.model,
			Instructions: openai.String(strings.Join(instructions, "\n")),
			Input: responses.ResponseNewParamsInputUnion{
				OfString: openai.String(strings.Join(inputs, "\n")),
			},
		})
		if err != nil {
			yield(nil, fmt.Errorf("openai responses.new: %w", err))
			return
		}

		if !yield(&model.LLMResponse{
			Content: genai.NewContentFromText(resp.OutputText(), genai.RoleModel),
		}, nil) {
			return
		}
	}
}

// NewOpenAI creates an OpenAI model.
func NewOpenAI(cfg ModelConfig) (model.LLM, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(time.Duration(cfg.Timeout)*time.Second))
	}

	client := openai.NewClient(opts...)

	return &openAI{
		client: &client,
		model:  cfg.Model,
	}, nil
}
