package pdcataskmaster

import (
	"strings"
	"testing"
)

func TestRootInstructionDefinesStrictPDCA(t *testing.T) {
	t.Parallel()

	got := rootInstruction()
	for _, want := range []string{
		"strict PDCA workflow",
		"plan -> do -> check -> act",
		"Always start a new goal with plan.",
		"taskmaster.schedule_task",
		"taskmaster.finish",
		"session_id",
		"class: agent, transport: local, key: pdca-taskmaster",
		"The child agents available in this wrapper are plan, do, check, and act.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
}

func TestChildAgentInstructionsUsePDCAContract(t *testing.T) {
	t.Parallel()

	checkInstruction := childAgentInstructions["check"]
	if !strings.Contains(checkInstruction, "strict PDCA flow") {
		t.Fatalf("check instruction = %q, want PDCA guidance", checkInstruction)
	}
	actInstruction := childAgentInstructions["act"]
	if !strings.Contains(actInstruction, "decision: close") || !strings.Contains(actInstruction, "decision: replan") {
		t.Fatalf("act instruction = %q, want advisory close/replan guidance", actInstruction)
	}
}
