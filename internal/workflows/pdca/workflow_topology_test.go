package pdca

import (
	"iter"
	"slices"
	"testing"

	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

func TestNewLoopAgentUsesOnlyOrchestratorSubAgent(t *testing.T) {
	t.Parallel()

	orchestrator, err := agent.New(agent.Config{
		Name:        "NormaLoopAgent",
		Description: "test orchestrator",
		Run: func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}

	loop, err := newLoopAgent(3, orchestrator)
	if err != nil {
		t.Fatalf("newLoopAgent() error = %v", err)
	}

	subAgents := loop.SubAgents()
	if len(subAgents) != 1 {
		t.Fatalf("len(loop.SubAgents()) = %d, want 1", len(subAgents))
	}
	if subAgents[0].Name() != orchestrator.Name() {
		t.Fatalf("loop sub-agent = %q, want %q", subAgents[0].Name(), orchestrator.Name())
	}
}

func TestNewNormaLoopAgentRegistersRoleSubAgents(t *testing.T) {
	t.Parallel()

	stepIndex := 0
	loopAgent, err := NewNormaLoopAgent(
		config.Config{},
		nil,
		nil,
		workflows.RunInput{},
		&stepIndex,
		"",
	)
	if err != nil {
		t.Fatalf("NewNormaLoopAgent() error = %v", err)
	}

	subAgents := loopAgent.SubAgents()
	if len(subAgents) != 4 {
		t.Fatalf("len(loopAgent.SubAgents()) = %d, want 4", len(subAgents))
	}

	gotNames := make([]string, 0, len(subAgents))
	for _, subAgent := range subAgents {
		gotNames = append(gotNames, subAgent.Name())
	}
	wantNames := []string{RolePlan, RoleDo, RoleCheck, RoleAct}
	for _, want := range wantNames {
		if !slices.Contains(gotNames, want) {
			t.Fatalf("missing sub-agent %q, got %v", want, gotNames)
		}
	}
}
