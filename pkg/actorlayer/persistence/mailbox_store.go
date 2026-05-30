package persistence

import (
	"context"
	"time"

	"github.com/normahq/norma/pkg/actorlayer"
)

// MailboxStore defines persistent mailbox operations for at-least-once delivery.
// MVP runtime can ignore this store; production implementations can wire it in later.
type MailboxStore interface {
	Append(ctx context.Context, msg StoredEnvelope) error
	Dequeue(ctx context.Context, actorID actorlayer.ActorID, limit int) ([]StoredEnvelope, error)
	Ack(ctx context.Context, actorID actorlayer.ActorID, messageIDs []actorlayer.MessageID) error
	Nack(ctx context.Context, actorID actorlayer.ActorID, messageIDs []actorlayer.MessageID, retryAt time.Time) error
}

// StoredEnvelope is the persistence representation of one mailbox entry.
type StoredEnvelope struct {
	ActorID     actorlayer.ActorID
	Envelope    actorlayer.Envelope
	Attempts    int
	VisibleAt   time.Time
	PersistedAt time.Time
}
