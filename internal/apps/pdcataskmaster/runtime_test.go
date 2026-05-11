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
		"target values are plan, do, check, and act",
		"Child outcomes are routed back to you asynchronously by runtime policy.",
		"The child agents available in this wrapper are plan, do, check, and act.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "task_id") {
		t.Fatalf("rootInstruction() = %q, do not want task_id in public contract", got)
	}
	for _, unwanted := range []string{
		"class: agent, transport: local, key: pdca-taskmaster",
		"locator",
		"report_to",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
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
	if !strings.Contains(actInstruction, "decision: close") || !strings.Contains(actInstruction, "decision: replan") || !strings.Contains(actInstruction, "stop or replan") {
		t.Fatalf("act instruction = %q, want advisory close/replan guidance", actInstruction)
	}
}
