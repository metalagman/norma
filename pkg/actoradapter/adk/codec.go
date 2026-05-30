package adk

import (
	"encoding/json"
	"fmt"

	"github.com/normahq/norma/pkg/actorlayer"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Codec maps actor envelopes to ADK content and ADK events back to payloads.
type Codec interface {
	ToContent(env actorlayer.Envelope) (*genai.Content, error)
	FromEvent(event *session.Event) (any, error)
	FinalResponse(events []*session.Event) (any, bool, error)
}

type textCodec struct{}

// TextCodec returns a codec that serializes payloads into user-role text content.
func TextCodec() Codec {
	return textCodec{}
}

func (textCodec) ToContent(env actorlayer.Envelope) (*genai.Content, error) {
	switch payload := env.Payload.(type) {
	case *genai.Content:
		return payload, nil
	case genai.Content:
		return &payload, nil
	case string:
		return genai.NewContentFromText(payload, genai.RoleUser), nil
	case []byte:
		return genai.NewContentFromText(string(payload), genai.RoleUser), nil
	case fmt.Stringer:
		return genai.NewContentFromText(payload.String(), genai.RoleUser), nil
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		return genai.NewContentFromText(string(raw), genai.RoleUser), nil
	}
}

func (textCodec) FromEvent(event *session.Event) (any, error) {
	if text := visibleText(event); text != "" {
		return text, nil
	}
	return event, nil
}

func (textCodec) FinalResponse(events []*session.Event) (any, bool, error) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if !ev.IsFinalResponse() {
			continue
		}
		if text := visibleText(ev); text != "" {
			return text, true, nil
		}
		if ev.Content != nil {
			return ev.Content, true, nil
		}
		return ev, true, nil
	}
	return nil, false, nil
}

type jsonCodec struct{}

// JSONCodec returns a codec that encodes payloads as JSON text.
func JSONCodec() Codec {
	return jsonCodec{}
}

func (jsonCodec) ToContent(env actorlayer.Envelope) (*genai.Content, error) {
	if content, ok := env.Payload.(*genai.Content); ok {
		return content, nil
	}
	raw, err := json.Marshal(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return genai.NewContentFromText(string(raw), genai.RoleUser), nil
}

func (jsonCodec) FromEvent(event *session.Event) (any, error) {
	text := visibleText(event)
	if text == "" {
		return event, nil
	}
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return text, nil
	}
	return out, nil
}

func (jsonCodec) FinalResponse(events []*session.Event) (any, bool, error) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || !ev.IsFinalResponse() {
			continue
		}
		text := visibleText(ev)
		if text == "" {
			return ev, true, nil
		}
		var out any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			return nil, false, fmt.Errorf("decode final JSON response: %w", err)
		}
		return out, true, nil
	}
	return nil, false, nil
}

type contentCodec struct{}

// ContentCodec returns a codec that requires genai.Content payloads.
func ContentCodec() Codec {
	return contentCodec{}
}

func (contentCodec) ToContent(env actorlayer.Envelope) (*genai.Content, error) {
	switch payload := env.Payload.(type) {
	case *genai.Content:
		return payload, nil
	case genai.Content:
		return &payload, nil
	default:
		return nil, fmt.Errorf("content codec requires genai.Content payload, got %T", env.Payload)
	}
}

func (contentCodec) FromEvent(event *session.Event) (any, error) {
	return event, nil
}

func (contentCodec) FinalResponse(events []*session.Event) (any, bool, error) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || !ev.IsFinalResponse() {
			continue
		}
		if ev.Content != nil {
			return ev.Content, true, nil
		}
		return ev, true, nil
	}
	return nil, false, nil
}
