package structuredagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"text/template"

	"github.com/rs/zerolog"
	"github.com/xeipuuv/gojsonschema"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

const (
	defaultWrapperName               = "StructuredWrappedAgent"
	defaultMaxAccumulatedOutputBytes = 1024 * 1024
	validationFailurePreviewLimit    = 4096

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

	promptTemplate = `{{ if .SystemInstruction }}{{ .SystemInstruction }}

{{ end }}I/O Requirements:
{{ if .InputSchema }}
- Read input JSON schema (text below).
{{ else }}
- Read input text (text below).
{{ end }}
{{ if .OutputSchema }}
- Read output JSON schema (text below).
- Produce output JSON that conforms to the output schema.

STRICT OUTPUT CONTRACT:
- MUST return exactly one JSON object that matches the output schema.
- MUST start the JSON object with "{" at byte-start OR at the beginning of a new line (column 1).
- MUST NOT include any text before the JSON object.
- MUST NOT include any text after the JSON object.
- MUST NOT include markdown code fences, headings, explanations, or commentary.

Invalid example (DO NOT DO THIS):
Let me explore first.
{"status":"ok"}

Valid example:
{"status":"ok"}
{{ else }}
- Produce a direct text response.
{{ end }}

{{ if .InputSchema }}
Input JSON Schema:
{{ .InputSchema }}
{{ end }}

{{ if .OutputSchema }}
Output JSON Schema:
{{ .OutputSchema }}
{{ end }}

{{ if .InputSchema }}Input JSON:{{ else }}Input:{{ end }}
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
	systemInstruction         string
	inputSchema               string
	outputSchema              string
	validateInput             bool
	validateOutput            bool
	maxAccumulatedOutputBytes int
	outputValidationRetries   int
}

type options struct {
	inputSchema               string
	outputSchema              string
	validateInput             bool
	validateOutput            bool
	systemInstruction         string
	maxAccumulatedOutputBytes int
	outputValidationRetries   int
}

// Option customizes wrapper behavior at creation time.
type Option func(*options)

// WithInputSchema overrides default input JSON schema.
func WithInputSchema(inputSchema string) Option {
	return func(o *options) {
		o.inputSchema = strings.TrimSpace(inputSchema)
		o.validateInput = true
	}
}

// WithOutputSchema overrides default output JSON schema.
func WithOutputSchema(outputSchema string) Option {
	return func(o *options) {
		o.outputSchema = strings.TrimSpace(outputSchema)
		o.validateOutput = true
	}
}

// WithoutInputSchema disables input JSON schema validation and forwards the raw
// user text into the structured wrapper prompt.
func WithoutInputSchema() Option {
	return func(o *options) {
		o.inputSchema = ""
		o.validateInput = false
	}
}

// WithoutOutputSchema disables output JSON extraction and schema validation.
// The wrapped agent's accumulated text is returned as-is.
func WithoutOutputSchema() Option {
	return func(o *options) {
		o.outputSchema = ""
		o.validateOutput = false
	}
}

// WithSystemInstruction sets the system instruction for the agent.
// This instruction is prepended to the I/O requirements prompt.
func WithSystemInstruction(instruction string) Option {
	return func(o *options) {
		o.systemInstruction = strings.TrimSpace(instruction)
	}
}

// WithMaxAccumulatedOutputBytes sets the maximum number of output text bytes
// accumulated for schema validation in a single turn.
func WithMaxAccumulatedOutputBytes(maxBytes int) Option {
	return func(o *options) {
		o.maxAccumulatedOutputBytes = maxBytes
	}
}

// WithOutputValidationRetries sets the number of retries for output schema validation failures.
// Default is 1 retry (2 total attempts).
func WithOutputValidationRetries(retries int) Option {
	return func(o *options) {
		if retries < 0 {
			retries = 0
		}
		o.outputValidationRetries = retries
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
		validateInput:             true,
		validateOutput:            true,
		maxAccumulatedOutputBytes: defaultMaxAccumulatedOutputBytes,
		outputValidationRetries:   1,
	}
	for _, set := range setters {
		if set == nil {
			continue
		}
		set(&opts)
	}
	if opts.validateInput && strings.TrimSpace(opts.inputSchema) == "" {
		opts.inputSchema = inputSchemaJSON
	}
	if opts.validateOutput && strings.TrimSpace(opts.outputSchema) == "" {
		opts.outputSchema = outputSchemaJSON
	}
	if opts.maxAccumulatedOutputBytes <= 0 {
		opts.maxAccumulatedOutputBytes = defaultMaxAccumulatedOutputBytes
	}
	if opts.outputValidationRetries < 0 {
		opts.outputValidationRetries = 0
	}
	if opts.validateInput {
		if err := validateSchemaDefinition(opts.inputSchema, "input"); err != nil {
			return nil, err
		}
	}
	if opts.validateOutput {
		if err := validateSchemaDefinition(opts.outputSchema, "output"); err != nil {
			return nil, err
		}
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
		systemInstruction:         opts.systemInstruction,
		inputSchema:               opts.inputSchema,
		outputSchema:              opts.outputSchema,
		validateInput:             opts.validateInput,
		validateOutput:            opts.validateOutput,
		maxAccumulatedOutputBytes: opts.maxAccumulatedOutputBytes,
		outputValidationRetries:   opts.outputValidationRetries,
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

		if w.validateInput {
			if err := validateInputSchema(w.inputSchema, rawInput); err != nil {
				logger.Debug().Err(err).Msg("structured wrapper input validation failed")
				yield(nil, fmt.Errorf("validate structured input: %w", err))
				return
			}
		}

		prompt, err := buildPrompt(promptData{
			Input:             rawInput,
			InputSchema:       w.inputSchema,
			OutputSchema:      w.outputSchema,
			SystemInstruction: w.systemInstruction,
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

		accumulatedText, totalEvents, textEventCount, sawTurnComplete, err := w.collectWrappedOutput(wrappedCtx)
		if err != nil {
			yield(nil, err)
			return
		}
		if !sawTurnComplete {
			logger.Debug().
				Str("invocation_id", ctx.InvocationID()).
				Msg("wrapped agent did not emit turn_complete; appended synthetic turn_complete event")
		}
		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("inner_event_count", totalEvents).
			Int("text_event_count", textEventCount).
			Bool("saw_turn_complete", sawTurnComplete).
			Msg("collected inner agent events")

		logger.Debug().
			Str("invocation_id", ctx.InvocationID()).
			Int("accumulated_output_len", len(accumulatedText)).
			Str("accumulated_output_preview", truncateForLog(accumulatedText, 320)).
			Msg("collected accumulated output from inner agent")

		if !w.validateOutput {
			outputEvent := session.NewEvent(context.Background(), ctx.InvocationID())
			outputEvent.Content = genai.NewContentFromText(accumulatedText, genai.RoleModel)
			if !yield(outputEvent, nil) {
				return
			}

			turnComplete := session.NewEvent(context.Background(), ctx.InvocationID())
			turnComplete.TurnComplete = true
			if !yield(turnComplete, nil) {
				return
			}
			return
		}

		var jsonOutput string
		var lastOutputErr error
		maxAttempts := w.outputValidationRetries + 1
		attemptsPerformed := 0

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			attemptsPerformed = attempt
			logger.Debug().
				Str("invocation_id", ctx.InvocationID()).
				Int("attempt", attempt).
				Int("max_attempts", maxAttempts).
				Msg("validating output schema")

			jsonOutput, lastOutputErr = extractAndValidateOutputJSON(w.outputSchema, accumulatedText)
			if lastOutputErr == nil {
				logger.Debug().
					Str("invocation_id", ctx.InvocationID()).
					Int("attempt", attempt).
					Msg("output schema validation succeeded")
				break
			}

			if !errors.Is(lastOutputErr, ErrStructuredOutputSchemaValidation) {
				logger.Debug().
					Err(lastOutputErr).
					Str("invocation_id", ctx.InvocationID()).
					Int("attempt", attempt).
					Int("accumulated_output_len", len(accumulatedText)).
					Str("validation_json_full", validationJSONForLog(accumulatedText)).
					Msg("non-retryable validation error")
				break
			}

			if attempt < maxAttempts {
				logger.Debug().
					Err(lastOutputErr).
					Str("invocation_id", ctx.InvocationID()).
					Int("attempt", attempt).
					Int("max_attempts", maxAttempts).
					Int("accumulated_output_len", len(accumulatedText)).
					Str("validation_json_full", validationJSONForLog(accumulatedText)).
					Msg("output schema validation failed, retrying")

				accumulatedText, _, _, _, lastOutputErr = w.collectWrappedOutput(wrappedCtx)
			}
		}

		if lastOutputErr != nil {
			logger.Debug().
				Err(lastOutputErr).
				Str("invocation_id", ctx.InvocationID()).
				Int("attempts", attemptsPerformed).
				Int("max_attempts", maxAttempts).
				Int("accumulated_output_len", len(accumulatedText)).
				Str("validation_json_full", validationJSONForLog(accumulatedText)).
				Msg("output schema validation failed after all retries")
			yield(nil, fmt.Errorf("validate structured output: %w", &OutputValidationError{
				Err:               lastOutputErr,
				AccumulatedOutput: accumulatedText,
			}))
			return
		}

		outputEvent := session.NewEvent(context.Background(), ctx.InvocationID())
		outputEvent.Content = genai.NewContentFromText(jsonOutput, genai.RoleModel)
		if !yield(outputEvent, nil) {
			return
		}

		turnComplete := session.NewEvent(context.Background(), ctx.InvocationID())
		turnComplete.TurnComplete = true
		if !yield(turnComplete, nil) {
			return
		}
	}
}

func (w *wrapperAgent) collectWrappedOutput(ctx adkagent.InvocationContext) (string, int, int, bool, error) {
	var partialText strings.Builder
	finalText := ""
	sawFinalText := false
	sawTurnComplete := false
	totalEvents := 0
	textEventCount := 0

	for ev, err := range w.wrapped.Run(ctx) {
		if err != nil {
			return "", totalEvents, textEventCount, sawTurnComplete, err
		}
		if ev == nil {
			continue
		}
		totalEvents++
		text := eventText(ev)

		if text != "" {
			if ev.TurnComplete && !ev.Partial {
				if len(text) > w.maxAccumulatedOutputBytes {
					return "", totalEvents, textEventCount, sawTurnComplete,
						fmt.Errorf("accumulated output exceeds limit: %d bytes", w.maxAccumulatedOutputBytes)
				}
				finalText = text
				sawFinalText = true
			} else {
				if partialText.Len()+len(text) > w.maxAccumulatedOutputBytes {
					return "", totalEvents, textEventCount, sawTurnComplete,
						fmt.Errorf("accumulated output exceeds limit: %d bytes", w.maxAccumulatedOutputBytes)
				}
				partialText.WriteString(text)
			}
			textEventCount++
		}

		if ev.TurnComplete {
			sawTurnComplete = true
			break
		}
	}

	if sawFinalText {
		return finalText, totalEvents, textEventCount, sawTurnComplete, nil
	}
	return partialText.String(), totalEvents, textEventCount, sawTurnComplete, nil
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

func (c wrapperInvocationContext) WithICDelta(d *adkagent.InvocationContextDelta) adkagent.InvocationContext {
	c.InvocationContext = c.InvocationContext.WithICDelta(d)
	return c
}

type promptData struct {
	Input             string
	InputSchema       string
	OutputSchema      string
	SystemInstruction string
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
	if err := validateJSONAgainstSchema(rawInput, schema, "input"); err != nil {
		return errors.Join(ErrStructuredInputSchemaValidation, err)
	}
	return nil
}

func extractAndValidateOutputJSON(schema, rawOutput string) (string, error) {
	candidate, err := extractOutputJSON(rawOutput)
	if err != nil {
		return "", errors.Join(
			ErrStructuredOutputSchemaValidation,
			fmt.Errorf("extract output JSON: %w", err),
		)
	}
	if err := validateJSONAgainstSchema(candidate, schema, "output"); err != nil {
		return "", errors.Join(ErrStructuredOutputSchemaValidation, err)
	}
	return candidate, nil
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

func extractOutputJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("output is empty")
	}

	idx := firstLineStartedJSONObject(raw)
	if idx == -1 {
		return "", fmt.Errorf("no JSON object found at byte start or line start")
	}

	endIdx, err := findJSONObjectEnd(raw, idx)
	if err != nil {
		return "", err
	}
	if endIdx < idx {
		return "", fmt.Errorf("JSON payload is empty")
	}

	trailing := strings.TrimSpace(raw[endIdx+1:])
	if trailing != "" && trailing != "```" {
		return "", fmt.Errorf("non-whitespace content after JSON object")
	}

	return raw[idx : endIdx+1], nil
}

func firstLineStartedJSONObject(text string) int {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if i == 0 || text[i-1] == '\n' {
			return i
		}
	}
	return -1
}

func findJSONObjectEnd(text string, start int) (int, error) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return -1, fmt.Errorf("JSON payload is empty")
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}

	return -1, fmt.Errorf("unterminated JSON object")
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

func validationJSONForLog(raw string) string {
	if raw == "" {
		return raw
	}
	start := firstLineStartedJSONObject(raw)
	if start == -1 {
		return raw
	}
	return raw[start:]
}
