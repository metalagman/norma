package actorlayer

import "time"

type tellOptions struct {
	from          *Ref
	replyTo       *Ref
	headers       map[string]string
	correlationID CorrelationID
	messageID     MessageID
	deadline      time.Time
}

// TellOption configures Tell delivery metadata.
type TellOption interface{ applyTell(opts *tellOptions) }

type tellOptionFunc func(*tellOptions)

func (f tellOptionFunc) applyTell(o *tellOptions) { f(o) }

// WithFrom sets the sender reference on an outgoing envelope.
func WithFrom(from Ref) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		o.from = &from
	})
}

// WithReplyTo sets the reply target reference on an outgoing envelope.
func WithReplyTo(replyTo Ref) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		o.replyTo = &replyTo
	})
}

// WithHeader sets one envelope header entry.
func WithHeader(key, value string) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers[key] = value
	})
}

// WithHeaders merges envelope header entries.
func WithHeaders(headers map[string]string) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		if len(headers) == 0 {
			return
		}
		if o.headers == nil {
			o.headers = make(map[string]string, len(headers))
		}
		for k, v := range headers {
			o.headers[k] = v
		}
	})
}

// WithCorrelationID sets envelope correlation id.
func WithCorrelationID(id CorrelationID) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		o.correlationID = id
	})
}

// WithMessageID sets envelope message id.
func WithMessageID(id MessageID) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		o.messageID = id
	})
}

// WithDeadline sets envelope processing deadline.
func WithDeadline(deadline time.Time) TellOption {
	return tellOptionFunc(func(o *tellOptions) {
		o.deadline = deadline
	})
}

type askOptions struct {
	tellOptions
	timeout time.Duration
}

// AskOption configures Ask behavior and forwarded envelope metadata.
type AskOption interface{ applyAsk(opts *askOptions) }

type askOptionFunc func(*askOptions)

func (f askOptionFunc) applyAsk(o *askOptions) { f(o) }

// WithTimeout sets Ask timeout.
func WithTimeout(timeout time.Duration) AskOption {
	return askOptionFunc(func(o *askOptions) {
		o.timeout = timeout
	})
}

// WithAskHeader sets one header on Ask forwarded envelope.
func WithAskHeader(key, value string) AskOption {
	return askOptionFunc(func(o *askOptions) {
		if o.headers == nil {
			o.headers = make(map[string]string)
		}
		o.headers[key] = value
	})
}

// WithAskHeaders merges headers on Ask forwarded envelope.
func WithAskHeaders(headers map[string]string) AskOption {
	return askOptionFunc(func(o *askOptions) {
		if len(headers) == 0 {
			return
		}
		if o.headers == nil {
			o.headers = make(map[string]string, len(headers))
		}
		for k, v := range headers {
			o.headers[k] = v
		}
	})
}

// WithAskFrom sets sender reference on Ask forwarded envelope.
func WithAskFrom(from Ref) AskOption {
	return askOptionFunc(func(o *askOptions) {
		o.from = &from
	})
}

// WithAskCorrelationID sets Ask forwarded envelope correlation id.
func WithAskCorrelationID(id CorrelationID) AskOption {
	return askOptionFunc(func(o *askOptions) {
		o.correlationID = id
	})
}

type spawnOptions struct {
	actorID string
}

// SpawnOption configures Spawn behavior.
type SpawnOption interface{ applySpawn(opts *spawnOptions) }

type spawnOptionFunc func(*spawnOptions)

func (f spawnOptionFunc) applySpawn(o *spawnOptions) { f(o) }

// WithActorID sets an explicit actor id instead of generated id.
func WithActorID(id string) SpawnOption {
	return spawnOptionFunc(func(o *spawnOptions) {
		o.actorID = id
	})
}
