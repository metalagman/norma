package engine

import (
	"fmt"

	actorlayer "github.com/normahq/norma/v2/pkg/actorlayer"
)

var ErrActorNotFound = actorlayer.ErrActorNotFound

// ResolveError is returned when a dispatch address cannot be resolved.
// It wraps ErrActorNotFound to support errors.Is checks while preserving
// the concrete address in messages and logs.
type ResolveError struct {
	Address string
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("%s: %s", ErrActorNotFound.Error(), e.Address)
}

func (e *ResolveError) Unwrap() error {
	return ErrActorNotFound
}
