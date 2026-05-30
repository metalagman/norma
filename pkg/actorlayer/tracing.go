package actorlayer

import "context"

// TraceAttrs is a lightweight attribute map for tracing spans and events.
type TraceAttrs map[string]string

// Span captures a traced operation.
type Span interface {
	AddEvent(name string, attrs TraceAttrs)
	End(err error)
}

// Tracer starts spans for actorlayer operations.
type Tracer interface {
	Start(ctx context.Context, name string, attrs TraceAttrs) (context.Context, Span)
}

// NopTracer is a tracer implementation that records nothing.
type NopTracer struct{}

// Start returns the input context and a no-op span.
func (NopTracer) Start(ctx context.Context, _ string, _ TraceAttrs) (context.Context, Span) {
	return ctx, nopSpan{}
}

type nopSpan struct{}

func (nopSpan) AddEvent(string, TraceAttrs) {}
func (nopSpan) End(error)                   {}
