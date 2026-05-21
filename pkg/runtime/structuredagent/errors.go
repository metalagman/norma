package structuredagent

import "errors"

// ErrStructuredIOSchemaValidation is the umbrella error for all structured I/O schema validation failures.
var ErrStructuredIOSchemaValidation = errors.New("structured I/O schema validation error")

// ErrStructuredInputSchemaValidation is returned when input JSON fails to match the expected schema.
// It satisfies errors.Is(err, ErrStructuredIOSchemaValidation).
var ErrStructuredInputSchemaValidation = errors.Join(
	errors.New("structured input schema validation error"),
	ErrStructuredIOSchemaValidation,
)

// ErrStructuredOutputSchemaValidation is returned when output JSON fails to match the expected schema.
// It satisfies errors.Is(err, ErrStructuredIOSchemaValidation).
var ErrStructuredOutputSchemaValidation = errors.Join(
	errors.New("structured output schema validation error"),
	ErrStructuredIOSchemaValidation,
)
