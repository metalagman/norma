package planner

import (
	"strings"
	"testing"
)

func TestPlannerInstruction_ContainsCodexBaseline(t *testing.T) {
	t.Parallel()

	got := plannerInstruction()
	if !strings.Contains(got, codexBaseInstruction) {
		t.Fatalf("plannerInstruction() missing codex baseline: %q", got)
	}
}

func TestPlannerInstruction_ContainsNormaPlannerPolicy(t *testing.T) {
	t.Parallel()

	got := plannerInstruction()
	for _, mustContain := range []string{
		"You are Norma's planning agent.",
		"Use MCP tasks tools ('norma.tasks.*')",
		"MCP 'norma.tasks.*' tools are the only source of truth for tasks, task status, and task relationships.",
		"Use 'parent-child' links for hierarchy only",
		"Never add a 'blocks' dependency between parent and child",
	} {
		if !strings.Contains(got, mustContain) {
			t.Fatalf("plannerInstruction() missing %q: %q", mustContain, got)
		}
	}
	for _, mustNotContain := range []string{
		"tracker CLI",
		"'human' tool",
	} {
		if strings.Contains(got, mustNotContain) {
			t.Fatalf("plannerInstruction() contains forbidden phrase %q: %q", mustNotContain, got)
		}
	}
	for _, token := range []string{
		string([]byte{98, 101, 97, 100, 115}),
		string([]byte{98, 100}),
	} {
		if strings.Contains(strings.ToLower(got), token) {
			t.Fatalf("plannerInstruction() contains forbidden token %q: %q", token, got)
		}
	}
}

func TestPlannerInstruction_ContainsCoordinatorRoutingGuidance(t *testing.T) {
	t.Parallel()

	got := plannerInstruction(Options{CoordinatorAssignee: "norma-coordinator"})
	for _, mustContain := range []string{
		"automatically assigned to the swarm coordinator assignee 'norma-coordinator'",
		"Use 'norma.tasks.set_assignee' only for deliberate rerouting after creation.",
	} {
		if !strings.Contains(got, mustContain) {
			t.Fatalf("plannerInstruction() missing %q: %q", mustContain, got)
		}
	}
}
