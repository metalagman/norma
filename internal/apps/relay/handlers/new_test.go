package handlers

import (
	"strings"
	"testing"
)

func TestBuildAgentWelcomeMessage_StartingWithoutSessionID(t *testing.T) {
	msg := BuildAgentWelcomeMessage("opencode", "", "desc", []string{"relay"})
	if !strings.Contains(msg, "Starting **opencode** agent session.") {
		t.Fatalf("message = %q, want starting text", msg)
	}
	if strings.Contains(msg, "session ()") {
		t.Fatalf("message = %q, must not include empty session id", msg)
	}
}

func TestBuildAgentWelcomeMessage_StartedWithSessionID(t *testing.T) {
	msg := BuildAgentWelcomeMessage("opencode", "tg-1-2", "desc", nil)
	if !strings.Contains(msg, "Started new **opencode** agent session (tg-1-2).") {
		t.Fatalf("message = %q, want started text with session id", msg)
	}
}
