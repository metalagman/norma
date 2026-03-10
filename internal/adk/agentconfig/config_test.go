package agentconfig

import "testing"

func TestHasSetModelSupport(t *testing.T) {
	t.Parallel()

	if HasSetModelSupport(AgentTypeCodexACP) {
		t.Fatalf("HasSetModelSupport(%q) = true, want false", AgentTypeCodexACP)
	}
	if !HasSetModelSupport(AgentTypeOpenCodeACP) {
		t.Fatalf("HasSetModelSupport(%q) = false, want true", AgentTypeOpenCodeACP)
	}
}
