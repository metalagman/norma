package persistence

import (
	"context"

	"github.com/normahq/norma/actorlayer"
)

// StateStore defines persistent actor state operations keyed by ActorID.
type StateStore interface {
	Load(ctx context.Context, actorID actorlayer.ActorID) (map[string]any, error)
	Save(ctx context.Context, actorID actorlayer.ActorID, state map[string]any) error
	Delete(ctx context.Context, actorID actorlayer.ActorID) error
}
