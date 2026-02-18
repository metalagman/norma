package modelfactory

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var _ model.LLM = (*openAI)(nil)

// openAI implements model.LLM using the OpenAI Chat Completions API.
type openAI struct {
	client *openai.Client
	model  string
}

// Name returns the model name.
func (m *openAI) Name() string {
	return "openai"
}

// GenerateContent executes a request against the OpenAI Chat Completions API.
func (m *openAI) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stream {
			yield(nil, fmt.Errorf("streaming is not supported by OpenAI Chat Completions API in this implementation"))
			return
		}

		params := openai.ChatCompletionNewParams{
			Model: m.model,
		}

		// Handle System Instruction
		if req.Config != nil && req.Config.SystemInstruction != nil {
			var systemParts []string
			for _, part := range req.Config.SystemInstruction.Parts {
				if part.Text != "" {
					systemParts = append(systemParts, part.Text)
				}
			}
			if len(systemParts) > 0 {
				params.Messages = append(params.Messages, openai.SystemMessage(strings.Join(systemParts, "\n")))
			}
		}

		// Handle Messages
		for _, content := range req.Contents {
			msg, err := toOpenAIMessage(content)
			if err != nil {
				yield(nil, fmt.Errorf("convert content to openai message: %w", err))
				return
			}
			params.Messages = append(params.Messages, msg)
		}

		// Handle Tools
		if req.Config != nil && len(req.Config.Tools) > 0 {
			for _, toolDef := range req.Config.Tools {
				if toolDef.FunctionDeclarations != nil {
					for _, fd := range toolDef.FunctionDeclarations {
						toolParam := openai.ChatCompletionToolParam{
							Type: "function",
							Function: shared.FunctionDefinitionParam{
								Name:        fd.Name,
								Description: openai.String(fd.Description),
							},
						}
						if fd.Parameters != nil {
							schemaBytes, err := json.Marshal(fd.Parameters)
							if err != nil {
								yield(nil, fmt.Errorf("marshal tool schema for %q: %w", fd.Name, err))
								return
							}
							var schemaMap map[string]any
							if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
								yield(nil, fmt.Errorf("unmarshal tool schema for %q: %w", fd.Name, err))
								return
							}
							toolParam.Function.Parameters = shared.FunctionParameters(schemaMap)
						}
						params.Tools = append(params.Tools, toolParam)
					}
				}
			}
		}

		resp, err := m.client.Chat.Completions.New(ctx, params)
		if err != nil {
			yield(nil, fmt.Errorf("openai chat.completions.new: %w", err))
			return
		}

		if len(resp.Choices) == 0 {
			yield(nil, fmt.Errorf("openai returned no choices"))
			return
		}

		choice := resp.Choices[0]
		genaiContent := fromOpenAIMessage(choice.Message)

		if !yield(&model.LLMResponse{
			Content: genaiContent,
		}, nil) {
			return
		}
	}
}

func toOpenAIMessage(content *genai.Content) (openai.ChatCompletionMessageParamUnion, error) {
	role := content.Role
	if role == "" {
		role = genai.RoleUser
	}

	switch role {
	case genai.RoleUser:
		var textParts []string
		for _, part := range content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		return openai.UserMessage(strings.Join(textParts, "\n")), nil

	case genai.RoleModel:
		var textParts []string
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, part := range content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
			if part.FunctionCall != nil {
				argsBytes, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("marshal function call args: %w", err)
				}
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
					ID:   tcID(part.FunctionCall.ID),
					Type: "function",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
			}
		}

		asst := openai.ChatCompletionAssistantMessageParam{
			Role: "assistant",
		}
		if len(textParts) > 0 {
			asst.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.String(strings.Join(textParts, "\n")),
			}
		}
		if len(toolCalls) > 0 {
			asst.ToolCalls = toolCalls
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &asst}, nil

	case "tool", "function":
		// ADK uses genai.RoleModel for function responses sometimes, or "tool" or "function"
		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				respBytes, err := json.Marshal(part.FunctionResponse.Response)
				if err != nil {
					return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("marshal function response: %w", err)
				}
				return openai.ToolMessage(string(respBytes), part.FunctionResponse.ID), nil
			}
		}
	}

	return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unsupported role: %s", role)
}

// tcID ensures we don't pass empty ID for tool calls.
func tcID(id string) string {
	if id == "" {
		return "call_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return id
}

func fromOpenAIMessage(msg openai.ChatCompletionMessage) *genai.Content {
	content := &genai.Content{
		Role: genai.RoleModel,
	}

	if msg.Content != "" {
		content.Parts = append(content.Parts, &genai.Part{
			Text: msg.Content,
		})
	}

	for _, tc := range msg.ToolCalls {
		if tc.Type == "function" {
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   tc.ID,
					Name: tc.Function.Name,
					Args: args,
				},
			})
		}
	}

	return content
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
