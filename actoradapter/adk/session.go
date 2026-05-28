package adk

import (
	"fmt"

	"github.com/normahq/norma/actorlayer"
)

// SessionPolicy resolves the ADK session ID for an incoming envelope.
type SessionPolicy interface {
	SessionID(env actorlayer.Envelope, self actorlayer.Ref) string
}

// UserPolicy resolves ADK user IDs from actor envelopes.
type UserPolicy interface {
	UserID(env actorlayer.Envelope) string
}

// StaticUserPolicy always returns a fixed user ID.
type StaticUserPolicy string

// UserID returns the configured static user ID.
func (p StaticUserPolicy) UserID(_ actorlayer.Envelope) string {
	return string(p)
}

// HeaderUserPolicy extracts user ID from envelope headers.
type HeaderUserPolicy struct {
	HeaderName string
	Fallback   string
}

// HeaderUser builds a UserPolicy that reads one header with fallback.
func HeaderUser(headerName, fallback string) UserPolicy {
	if fallback == "" {
		fallback = "system"
	}
	return HeaderUserPolicy{HeaderName: headerName, Fallback: fallback}
}

// UserID returns header value when present, else fallback.
func (p HeaderUserPolicy) UserID(env actorlayer.Envelope) string {
	if p.HeaderName != "" && env.Headers != nil {
		if value := env.Headers[p.HeaderName]; value != "" {
			return value
		}
	}
	return p.Fallback
}

type sessionPolicyFunc func(env actorlayer.Envelope, self actorlayer.Ref) string

func (f sessionPolicyFunc) SessionID(env actorlayer.Envelope, self actorlayer.Ref) string {
	return f(env, self)
}

// ConversationSession resolves session IDs from a conversation header, correlation ID, or message ID.
func ConversationSession(headerName string) SessionPolicy {
	if headerName == "" {
		headerName = "conversation_id"
	}
	return sessionPolicyFunc(func(env actorlayer.Envelope, self actorlayer.Ref) string {
		if env.Headers != nil {
			if value := env.Headers[headerName]; value != "" {
				return value
			}
		}
		if env.CorrelationID != "" {
			return string(env.CorrelationID)
		}
		if env.ID != "" {
			return string(env.ID)
		}
		return fmt.Sprintf("actor-%s", self.ID())
	})
}

// PerActorSession uses the actor ID as the session ID.
func PerActorSession() SessionPolicy {
	return sessionPolicyFunc(func(_ actorlayer.Envelope, self actorlayer.Ref) string {
		return string(self.ID())
	})
}

// PerMessageSession uses message ID as session ID with actor fallback.
func PerMessageSession() SessionPolicy {
	return sessionPolicyFunc(func(env actorlayer.Envelope, self actorlayer.Ref) string {
		if env.ID != "" {
			return string(env.ID)
		}
		return fmt.Sprintf("msg-%s", self.ID())
	})
}
