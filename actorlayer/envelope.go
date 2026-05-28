package actorlayer

import (
	"fmt"
	"sync/atomic"
	"time"
)

// MessageID identifies one envelope.
type MessageID string

// CorrelationID groups related envelopes across exchanges.
type CorrelationID string

// Envelope is the transport unit placed in actor mailboxes.
type Envelope struct {
	ID            MessageID
	CorrelationID CorrelationID

	To      Ref
	From    Ref
	ReplyTo *Ref

	Headers map[string]string
	Payload any

	Deadline time.Time
	SentAt   time.Time
}

var envelopeSeq atomic.Uint64

func nextMessageID() MessageID {
	n := envelopeSeq.Add(1)
	return MessageID(fmt.Sprintf("msg-%d-%d", time.Now().UnixNano(), n))
}
