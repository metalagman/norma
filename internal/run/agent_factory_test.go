package run

import (
	"testing"
)

func TestAgentOutcomeDecisionField(t *testing.T) {
	t.Parallel()

	outcome := AgentOutcome{
		Status:   "passed",
		Verdict:  strPtr("pass"),
		Decision: strPtr("close"),
	}

	if outcome.Status != "passed" {
		t.Fatalf("Status = %q, want %q", outcome.Status, "passed")
	}
	if outcome.Verdict == nil || *outcome.Verdict != "pass" {
		t.Fatalf("Verdict = %v, want %q", outcome.Verdict, "pass")
	}
	if outcome.Decision == nil || *outcome.Decision != "close" {
		t.Fatalf("Decision = %v, want %q", outcome.Decision, "close")
	}
}

func TestAgentOutcomeDecisionFieldNil(t *testing.T) {
	t.Parallel()

	outcome := AgentOutcome{
		Status:   "stopped",
		Verdict:  nil,
		Decision: nil,
	}

	if outcome.Status != "stopped" {
		t.Fatalf("Status = %q, want %q", outcome.Status, "stopped")
	}
	if outcome.Verdict != nil {
		t.Fatalf("Verdict should be nil")
	}
	if outcome.Decision != nil {
		t.Fatalf("Decision should be nil")
	}
}

func strPtr(s string) *string {
	return &s
}
