package actorlayer

import "context"

// DeadLetter describes a delivery that could not be completed.
type DeadLetter struct {
	Envelope Envelope
	Reason   string
	Err      error
}

// DeadLetterSink consumes failed delivery records.
type DeadLetterSink interface {
	HandleDeadLetter(ctx context.Context, letter DeadLetter)
}

// NopDeadLetterSink ignores all dead letters.
type NopDeadLetterSink struct{}

// HandleDeadLetter discards the dead letter.
func (NopDeadLetterSink) HandleDeadLetter(_ context.Context, _ DeadLetter) {}
