package acpagent

import (
	"errors"
	"testing"

	upstream "github.com/normahq/go-adk-acpagent/v2"
)

func TestAliasesUseUpstreamValues(t *testing.T) {
	if SessionStateKey != upstream.SessionStateKey {
		t.Fatalf("SessionStateKey = %q, want %q", SessionStateKey, upstream.SessionStateKey)
	}
	if PlanStateKey != upstream.PlanStateKey {
		t.Fatalf("PlanStateKey = %q, want %q", PlanStateKey, upstream.PlanStateKey)
	}
	if CWDStateKey != upstream.CWDStateKey {
		t.Fatalf("CWDStateKey = %q, want %q", CWDStateKey, upstream.CWDStateKey)
	}
	if !errors.Is(ErrPromptAlreadyActive, upstream.ErrPromptAlreadyActive) {
		t.Fatalf("ErrPromptAlreadyActive = %v, want upstream error", ErrPromptAlreadyActive)
	}
}
