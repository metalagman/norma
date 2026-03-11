package structured

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"sort"
	"strings"
	"text/template"

	"github.com/rs/zerolog"
	"github.com/xeipuuv/gojsonschema"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultWrapperName               = "StructuredWrappedAgent"
	defaultMaxAccumulatedOutputBytes = 1024 * 1024

	inputSchemaJSON = `{
  "type": "object",
  "properties": {
    "input": {"type": "string"}
  },
  "required": ["input"]
}`

	outputSchemaJSON = `{
  "type": "object",
  "properties": {
    "output": {"type": "string"}
  },
  "required": ["output"]
}`

	promptTemplate = `I/O Requirements:
- Read input JSON schema (text below).
- Read output JSON schema (text below).
- Read input JSON content (text below).
- Produce output JSON that conforms to the output schema.
- Return only output JSON text.

Input JSON Schema:
{{ .InputSchema }}

Output JSON Schema:
{{ .OutputSchema }}

	Input JSON:
{{ .Input }}
`
)

var parsedPromptTemplate = template.Must(template.New("structured-wrapper-prompt").Parse(promptTemplate))

type wrapperAgent struct {
	// Agent is embedded to satisfy ADK's sealed internal() method on the
	// interface while this wrapper overrides Name/Description/Run/SubAgents.
	adkagent.Agent
	name                      string
	description               string
	wrapped                   adkagent.Agent
	inputSchema               string
	outputSchema              string
	maxAccumulatedOutputBytes int
}

type options struct {
	inputSchema               string
	outputSchema              string
	maxAccumulatedOutputBytes int
}

// Option customizes wrapper behavior at creation time.
type Option func(*options)

// WithInputSchema overrides default input JSON schema.
func WithInputSchema(inputSchema string) Option {
	return func(o *options) {
		o.inputSchema = strings.TrimSpace(inputSchema)
	}
}

// WithOutputSchema overrides default output JSON schema.
func WithOutputSchema(outputSchema string) Option {
	return func(o *options) {
		o.outputSchema = strings.TrimSpace(outputSchema)
	}
}

// WithMaxAccumulatedOutputBytes sets the maximum number of output text bytes
// accumulated for schema validation in a single turn.
func WithMaxAccumulatedOutputBytes(maxBytes int) Option {
	return func(o *options) {
		o.maxAccumulatedOutputBytes = maxBytes
	}
}

// NewAgent creates an ADK agent wrapper around another agent and validates
// structured input/output using configured schemas.
func NewAgent(wrapped adkagent.Agent, setters ...Option) (adkagent.Agent, error) {
	if wrapped == nil {
		return nil, fmt.Errorf("wrapped agent is required")
	}

	opts := options{
		inputSchema:               inputSchemaJSON,
		outputSchema:              outputSchemaJSON,
		maxAccumulatedOutputBytes: defaultMaxAccumulatedOutputBytes,
	}
	for _, set := range setters {
		if set == nil {
			continue
		}
		set(&opts)
	}
	if strings.TrimSpace(opts.inputSchema) == "" {
		opts.inputSchema = inputSchemaJSON
	}
	if strings.TrimSpace(opts.outputSchema) == "" {
		opts.outputSchema = outputSchemaJSON
	}
	if opts.maxAccumulatedOutputBytes <= 0 {
		opts.maxAccumulatedOutputBytes = defaultMaxAccumulatedOutputBytes
	}
	if err := validateSchemaDefinition(opts.inputSchema, "input"); err != nil {
		return nil, err
	}
	if err := validateSchemaDefinition(opts.outputSchema, "output"); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(wrapped.Name())
	if name == "" {
		name = defaultWrapperName
	} else {
		name += "_structured"
	}

	description := strings.TrimSpace(wrapped.Description())
	if description == "" {
		description = "Structured I/O wrapper around another ADK agent"
	}

	return &wrapperAgent{
		Agent:                     wrapped, // delegates ADK sealed internal() method
		name:                      name,
		description:               description,
		wrapped:                   wrapped,
		inputSchema:               opts.inputSchema,
		outputSchema:              opts.outputSchema,
		maxAccumulatedOutputBytes: opts.maxAccumulatedOutputBytes,
	}, nil
}

func (w *wrapperAgent) Name() string {
	return w.name
}

func (w *wrapperAgent) Description() string {
	return w.description
}

func (w *wrapperAgent) SubAgents() []adkagent.Agent {
	return []adkagent.Agent{w.wrapped}
}

func (w *wrapperAgent) Run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		logger := zerolog.Ctx(ctx).With().
			Str("subcomponent", "structured_wrapper_agent").
			Str("agent_name", w.Name()).
			Logger()

		rawInput := strings.TrimSpace(contentText(ctx.UserContent()))
		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("raw_input_len", len(rawInput)).
			Str("raw_input_preview", truncateForLog(rawInput, 320)).
			Msg("received structured wrapper input")

		if err := validateInputSchema(w.inputSchema, rawInput); err != nil {
			logger.Debug().Err(err).Msg("structured wrapper input validation failed")
			yield(nil, fmt.Errorf("validate structured input: %w", err))
			return
		}

		prompt, err := buildPrompt(promptData{
			Input:        rawInput,
			InputSchema:  w.inputSchema,
			OutputSchema: w.outputSchema,
		})
		if err != nil {
			yield(nil, fmt.Errorf("build structured prompt: %w", err))
			return
		}
		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("wrapped_prompt_len", len(prompt)).
			Str("wrapped_prompt_preview", truncateForLog(prompt, 320)).
			Msg("forwarding wrapped prompt to inner agent")

		wrappedCtx := wrapperInvocationContext{
			InvocationContext: ctx,
			agent:             w.wrapped,
			userContent:       genai.NewContentFromText(prompt, genai.RoleUser),
		}

		buffered := make([]*session.Event, 0, 8)
		var accumulated strings.Builder
		var turnComplete *session.Event
		sawTurnComplete := false
		totalEvents := 0
		passthroughEvents := 0

		for ev, err := range w.wrapped.Run(wrappedCtx) {
			if err != nil {
				yield(nil, err)
				return
			}
			if ev == nil {
				continue
			}
			totalEvents++

			if ev.TurnComplete {
				sawTurnComplete = true
				turnComplete = ev
				break
			}

			text := eventText(ev)
			if text != "" {
				if accumulated.Len()+len(text) > w.maxAccumulatedOutputBytes {
					yield(nil, fmt.Errorf("accumulated output exceeds limit: %d bytes", w.maxAccumulatedOutputBytes))
					return
				}
				buffered = append(buffered, ev)
				accumulated.WriteString(text)
				continue
			}

			passthroughEvents++
			if !yield(ev, nil) {
				return
			}
		}
		if !sawTurnComplete {
			turnComplete = session.NewEvent(ctx.InvocationID())
			turnComplete.TurnComplete = true
			logger.Debug().
				Str("invocation_id", ctx.InvocationID()).
				Msg("wrapped agent did not emit turn_complete; appended synthetic turn_complete event")
		}
		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("inner_event_count", totalEvents).
			Int("buffered_event_count", len(buffered)).
			Int("passthrough_event_count", passthroughEvents).
			Bool("saw_turn_complete", sawTurnComplete).
			Msg("collected inner agent events")

		accumulatedText := accumulated.String()
		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("accumulated_output_len", len(accumulatedText)).
			Str("accumulated_output_preview", truncateForLog(accumulatedText, 320)).
			Msg("collected accumulated output from inner agent")
		if err := validateOutputSchema(w.outputSchema, accumulatedText); err != nil {
			logger.Debug().Err(err).Msg("structured wrapper output validation failed")
			yield(nil, fmt.Errorf("validate structured output: %w", err))
			return
		}

		for _, ev := range buffered {
			if !yield(ev, nil) {
				return
			}
		}
		if turnComplete != nil {
			if !yield(turnComplete, nil) {
				return
			}
		}
	}
}

type wrapperInvocationContext struct {
	adkagent.InvocationContext
	agent       adkagent.Agent
	userContent *genai.Content
}

func (c wrapperInvocationContext) Agent() adkagent.Agent {
	return c.agent
}

func (c wrapperInvocationContext) UserContent() *genai.Content {
	return c.userContent
}

func (c wrapperInvocationContext) WithContext(ctx context.Context) adkagent.InvocationContext {
	return wrapperInvocationContext{
		InvocationContext: c.InvocationContext.WithContext(ctx),
		agent:             c.agent,
		userContent:       c.userContent,
	}
}

type promptData struct {
	Input        string
	InputSchema  string
	OutputSchema string
}

func buildPrompt(data promptData) (string, error) {
	var out bytes.Buffer
	if err := parsedPromptTemplate.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render prompt template: %w", err)
	}

	return out.String(), nil
}

// eventText extracts concatenated text from an event content.
func eventText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	return contentText(ev.Content)
}

// contentText extracts concatenated text from content parts.
func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Text == "" || part.Thought {
			continue
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func validateInputSchema(schema, rawInput string) error {
	return validateJSONAgainstSchema(rawInput, schema, "input")
}

func validateOutputSchema(schema, rawOutput string) error {
	return validateJSONAgainstSchema(rawOutput, schema, "output")
}

func validateJSONAgainstSchema(raw, schema, label string) error {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fmt.Errorf("%s is empty", label)
	}

	if strings.TrimSpace(schema) == "" {
		return fmt.Errorf("%s schema is empty", label)
	}

	result, err := gojsonschema.Validate(
		gojsonschema.NewStringLoader(schema),
		gojsonschema.NewStringLoader(text),
	)
	if err != nil {
		return fmt.Errorf("validate %s schema: %w", label, err)
	}
	if result.Valid() {
		return nil
	}

	validationErrs := make([]string, 0, len(result.Errors()))
	for _, schemaErr := range result.Errors() {
		validationErrs = append(validationErrs, schemaErr.String())
	}
	if len(validationErrs) == 0 {
		return fmt.Errorf("%s is not valid JSON", label)
	}
	sort.Strings(validationErrs)
	return fmt.Errorf("%s does not match schema: %s", label, strings.Join(validationErrs, "; "))
}

func validateSchemaDefinition(schema, label string) error {
	if strings.TrimSpace(schema) == "" {
		return fmt.Errorf("%s schema is empty", label)
	}
	if _, err := gojsonschema.NewSchema(gojsonschema.NewStringLoader(schema)); err != nil {
		return fmt.Errorf("%s schema invalid: %w", label, err)
	}
	return nil
}

func truncateForLog(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
