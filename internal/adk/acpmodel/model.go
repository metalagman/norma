// Package acpmodel provides an implementation of the ADK model.LLM interface
// that executes an external command using the Agent Client Protocol (ACP).
package acpmodel

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"

	"github.com/metalagman/norma/internal/adk/acpagent"
	"google.golang.org/adk/model"
)

var _ model.LLM = (*Model)(nil)

// Model implements model.LLM using acpagent.Agent.
type Model struct {
	agent *acpagent.Agent
	cfg   Config
}

// Config describes how to run the ACP model.
type Config struct {
	Name              string
	Description       string
	Model             string
	Command           []string
	WorkingDir        string
	Stderr            io.Writer
	PermissionHandler acpagent.PermissionHandler
	HasSetModel       bool
}

// New creates a new ACP model.
func New(cfg Config) (*Model, error) {
	if len(cfg.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	ag, err := acpagent.New(acpagent.Config{
		Name:              cfg.Name,
		Description:       cfg.Description,
		Model:             cfg.Model,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: cfg.PermissionHandler,
		HasSetModel:       cfg.HasSetModel,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create acp agent: %w", err)
	}
	return &Model{
		agent: ag,
		cfg:   cfg,
	}, nil
}

// Name returns the model name.
func (m *Model) Name() string {
	if m.cfg.Name != "" {
		return m.cfg.Name
	}
	if len(m.cfg.Command) > 0 {
		return m.cfg.Command[0]
	}
	return "acp"
}

// SetName sets the model name.
func (m *Model) SetName(name string) {
	m.cfg.Name = name
}

// Close closes the underlying ACP agent.
func (m *Model) Close() error {
	return m.agent.Close()
}

// GenerateContent executes the prompt using the ACP agent.
func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var systemPrompt string
		if req.Config != nil && req.Config.SystemInstruction != nil {
			var parts []string
			for _, part := range req.Config.SystemInstruction.Parts {
				if part.Text != "" {
					parts = append(parts, part.Text)
				}
			}
			systemPrompt = strings.Join(parts, "\n")
		}

		// ACP agent handles prepending system prompt if configured in its Agent.systemPrompt.
		// However, GenerateContent is a turn-based API. We should ideally pass the system prompt 
		// if acpagent supports it per-turn, or handle it here.
		// Current acpagent.Agent prepends its fixed systemPrompt. 
		// Here we'll combine what we got from LLMRequest.
		
		userPrompt := ""
		if len(req.Contents) > 0 {
			var parts []string
			for _, part := range req.Contents[0].Parts {
				if part.Text != "" {
					parts = append(parts, part.Text)
				}
			}
			userPrompt = strings.Join(parts, "\n")
		}

		if systemPrompt != "" {
			_ = systemPrompt + "\n\n" + userPrompt
		}

		// We need an InvocationContext to run the agent.
		// For now, we'll create a dummy one or refactor acpagent to not require it if possible.
		// Actually, acpagent.Agent.Run takes InvocationContext.
		// This Model implementation might need a way to wrap the request into an InvocationContext.
		// Since acpagent.Agent is an ADK Agent, we can use it directly if we have a session.
		
		yield(nil, fmt.Errorf("acpmodel.GenerateContent is not fully implemented: needs session integration"))
	}
}
