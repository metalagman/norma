package pdca

import (
	"encoding/json"
	"testing"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles/do"
	"github.com/metalagman/norma/internal/task"
)

func TestDoRoleMapRequestRefinesDefaultsToEmptySlice(t *testing.T) {
	role := GetRole(RoleDo)
	if role == nil {
		t.Fatal("GetRole(RoleDo) returned nil")
	}

	req := contracts.AgentRequest{
		Run:  contracts.RunInfo{ID: "run-1", Iteration: 1},
		Task: contracts.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{}},
		Step: contracts.StepInfo{Index: 2, Name: RoleDo},
		Paths: contracts.RequestPaths{
			WorkspaceDir: "/tmp",
			RunDir:       "/tmp",
		},
		Budgets: contracts.Budgets{
			MaxIterations:      1,
			MaxWallTimeMinutes: 10,
			MaxFailedChecks:    1,
		},
		Context: contracts.RequestContext{
			Facts: map[string]any{},
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Do: &do.DoInput{
			WorkPlan: &do.DoWorkPlan{
				TimeboxMinutes: 10,
				DoSteps:        []do.DoDoStep{},
				CheckSteps:     []do.DoCheckStep{},
				StopTriggers:   []string{},
			},
			AcceptanceCriteriaEffective: []do.DoEffectiveAC{
				{
					Id:     "AC-1",
					Origin: "baseline",
					Text:   "ok",
					Checks: []do.DoACCheck{
						{Id: "CHK-1", Cmd: "true", ExpectExitCodes: []int64{0}},
					},
				},
			},
		},
	}

	mapped, err := role.MapRequest(req)
	if err != nil {
		t.Fatalf("role.MapRequest() error = %v", err)
	}

	data, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("json.Marshal(mapped) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}

	doInput, ok := payload["do_input"].(map[string]any)
	if !ok {
		t.Fatalf("payload[\"do_input\"] type = %T, want map[string]any", payload["do_input"])
	}

	effectiveAny, ok := doInput["acceptance_criteria_effective"].([]any)
	if !ok {
		t.Fatalf("do_input[\"acceptance_criteria_effective\"] type = %T, want []any", doInput["acceptance_criteria_effective"])
	}
	if len(effectiveAny) != 1 {
		t.Fatalf("len(effectiveAny) = %d, want 1", len(effectiveAny))
	}

	ac, ok := effectiveAny[0].(map[string]any)
	if !ok {
		t.Fatalf("effectiveAny[0] type = %T, want map[string]any", effectiveAny[0])
	}

	refines, ok := ac["refines"].([]any)
	if !ok {
		t.Fatalf("ac[\"refines\"] type = %T, want []any (array, not null)", ac["refines"])
	}
	if len(refines) != 0 {
		t.Fatalf("len(refines) = %d, want 0", len(refines))
	}
}
