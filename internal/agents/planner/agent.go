package planner

import (
	"context"
	"fmt"
	"iter"
	"strings"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Options configures planner-specific wrapper behavior.
type Options struct {
	CoordinatorAssignee string
}

// New wraps a base ADK agent with planner-specific prompt/identity behavior.
func New(base adkagent.Agent, opts ...Options) (adkagent.Agent, error) {
	if base == nil {
		return nil, fmt.Errorf("base agent is required")
	}
	var cfg Options
	if len(opts) > 0 {
		cfg = opts[0]
	}

	desc := strings.TrimSpace(base.Description())
	if desc == "" {
		desc = "Planner-wrapped ADK agent"
	}

	return &wrappedAgent{
		Agent:       base,
		base:        base,
		name:        decoratedAgentName(base.Name()),
		description: desc,
		options:     cfg,
	}, nil
}

type wrappedAgent struct {
	// Embedded to satisfy ADK's sealed interface method.
	adkagent.Agent

	base        adkagent.Agent
	name        string
	description string
	options     Options
}

func (w *wrappedAgent) Name() string {
	return w.name
}

func (w *wrappedAgent) Description() string {
	return w.description
}

func (w *wrappedAgent) SubAgents() []adkagent.Agent {
	return []adkagent.Agent{w.base}
}

func (w *wrappedAgent) Run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	userPrompt := buildPlannerPrompt(contentText(ctx.UserContent()), w.options)

	wrappedCtx := plannerInvocationContext{
		InvocationContext: ctx,
		agent:             w.base,
		userContent:       genai.NewContentFromText(userPrompt, genai.RoleUser),
	}
	return w.base.Run(wrappedCtx)
}

func (w *wrappedAgent) Close() error {
	if closer, ok := w.base.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

type plannerInvocationContext struct {
	adkagent.InvocationContext
	agent       adkagent.Agent
	userContent *genai.Content
}

func (c plannerInvocationContext) Agent() adkagent.Agent {
	return c.agent
}

func (c plannerInvocationContext) UserContent() *genai.Content {
	return c.userContent
}

func (c plannerInvocationContext) WithContext(ctx context.Context) adkagent.InvocationContext {
	c.InvocationContext = c.InvocationContext.WithContext(ctx)
	return c
}

func (c plannerInvocationContext) WithICDelta(d *adkagent.InvocationContextDelta) adkagent.InvocationContext {
	c.InvocationContext = c.InvocationContext.WithICDelta(d)
	return c
}

func decoratedAgentName(baseName string) string {
	name := strings.TrimSpace(baseName)
	if name == "" {
		return "planner"
	}
	name = strings.TrimSuffix(name, "_agent")
	if strings.HasSuffix(name, "_planner") {
		return name
	}
	return name + "_planner"
}

func buildPlannerPrompt(userPrompt string, opts Options) string {
	userPrompt = strings.TrimSpace(userPrompt)
	systemPrompt := plannerInstruction(opts)
	if userPrompt == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + userPrompt
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range content.Parts {
		if part == nil || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.Text)
	}
	return strings.TrimSpace(b.String())
}
