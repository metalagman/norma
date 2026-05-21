package hostedagent

import (
	"fmt"
	"iter"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Config describes an ADK agent backed by a hosted model implementation.
type Config struct {
	// Name is the ADK agent display name.
	Name string
	// Description is the ADK agent description.
	Description string
	// Instruction is appended after GlobalInstruction for each model request.
	Instruction string
	// GlobalInstruction is prepended before Instruction for each model request.
	GlobalInstruction string
	// Model handles underlying content generation.
	Model model.LLM
}

type runtimeAgent struct {
	adkagent.Agent

	model             model.LLM
	instruction       string
	globalInstruction string
}

// New wraps a hosted model as an ADK agent.
func New(cfg Config) (adkagent.Agent, error) {
	if cfg.Model == nil {
		return nil, fmt.Errorf("model is required")
	}

	rt := &runtimeAgent{
		model:             cfg.Model,
		instruction:       strings.TrimSpace(cfg.Instruction),
		globalInstruction: strings.TrimSpace(cfg.GlobalInstruction),
	}

	base, err := adkagent.New(adkagent.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
		Run:         rt.run,
	})
	if err != nil {
		return nil, fmt.Errorf("create hosted agent: %w", err)
	}

	rt.Agent = base

	return rt, nil
}

func (a *runtimeAgent) run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		req := &model.LLMRequest{
			Model:    a.model.Name(),
			Contents: []*genai.Content{ctx.UserContent()},
			Config:   &genai.GenerateContentConfig{},
		}

		if instruction := joinInstructions(a.globalInstruction, a.instruction); instruction != "" {
			req.Config.SystemInstruction = genai.NewContentFromText(instruction, genai.RoleUser)
		}

		var finalText strings.Builder

		for resp, err := range a.model.GenerateContent(ctx, req, false) {
			if err != nil {
				yield(nil, fmt.Errorf("generate content: %w", err))
				return
			}

			if resp == nil || resp.Content == nil {
				continue
			}

			for _, part := range resp.Content.Parts {
				if part == nil || part.Text == "" {
					continue
				}

				finalText.WriteString(part.Text)
			}
		}

		ev := session.NewEvent(ctx.InvocationID())
		ev.Content = genai.NewContentFromText(finalText.String(), genai.RoleModel)
		ev.TurnComplete = true

		yield(ev, nil)
	}
}

func joinInstructions(values ...string) string {
	parts := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		parts = append(parts, value)
	}

	return strings.Join(parts, "\n\n")
}
