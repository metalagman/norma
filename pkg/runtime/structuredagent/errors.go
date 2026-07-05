package structuredagent

import (
	"errors"

	"github.com/normahq/go-adk-acpagent/providererror"
)

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

// OutputValidationError carries output validation context that callers may use
// for provider-aware error handling.
type OutputValidationError struct {
	Err               error
	AccumulatedOutput string
	ProviderError     *providererror.ProviderError
}

func (e *OutputValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return "structured output validation failed"
	}
	return e.Err.Error()
}

func (e *OutputValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
