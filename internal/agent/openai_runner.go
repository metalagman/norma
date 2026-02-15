package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/metalagman/norma/internal/agent/openaiapi"
	"github.com/metalagman/norma/internal/agents/pdca/models"
	"github.com/metalagman/norma/internal/config"
	"github.com/rs/zerolog/log"
)

type openAIRunner struct {
	role   models.Role
	client *openaiapi.Client
}

func newOpenAIRunner(cfg config.AgentConfig, role models.Role) (Runner, error) {
	client, err := openaiapi.NewClient(openaiapi.Config{
		Model:     cfg.Model,
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		APIKeyEnv: cfg.APIKeyEnv,
		Timeout:   time.Duration(cfg.Timeout) * time.Second,
	}, nil)
	if err != nil {
		return nil, err
	}

	return &openAIRunner{
		role:   role,
		client: client,
	}, nil
}

func (r *openAIRunner) Run(ctx context.Context, req models.AgentRequest, stdout, stderr io.Writer) ([]byte, []byte, int, error) {
	prompt, err := r.role.Prompt(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("generate prompt: %w", err)
	}

	if req.Paths.RunDir != "" {
		promptPath := filepath.Join(req.Paths.RunDir, "logs", "prompt.txt")
		_ = os.MkdirAll(filepath.Dir(promptPath), 0o755)
		if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
			log.Warn().Err(err).Str("path", promptPath).Msg("failed to save prompt log")
		}
	}

	input, err := r.role.MapRequest(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("map request: %w", err)
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("marshal input: %w", err)
	}

	out, err := r.client.Complete(ctx, openaiapi.CompletionRequest{
		Instructions: prompt,
		Input:        string(inputJSON),
	})
	if err != nil {
		if stderr != nil {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return nil, nil, 1, fmt.Errorf("run openai agent: %w", err)
	}

	rawOut := []byte(out.OutputText)
	if stdout != nil {
		_, _ = stdout.Write(rawOut)
	}

	agentResp, err := r.role.MapResponse(rawOut)
	if err != nil {
		if extracted, ok := ExtractJSON(rawOut); ok {
			agentResp, err = r.role.MapResponse(extracted)
		}
	}
	if err != nil {
		return nil, nil, 0, fmt.Errorf("parse agent response: %w", err)
	}

	normalizedOut, err := json.Marshal(agentResp)
	if err != nil {
		return rawOut, nil, 0, fmt.Errorf("marshal agent response: %w", err)
	}

	return normalizedOut, nil, 0, nil
}
