package normaloop

import (
	"encoding/json"
	"testing"

	"github.com/metalagman/norma/internal/task"
	"github.com/stretchr/testify/require"
)

func TestDoRoleMapRequestRefinesDefaultsToEmptySlice(t *testing.T) {
	role := &doRole{}

	req := AgentRequest{
		Run:  RunInfo{ID: "run-1", Iteration: 1},
		Task: TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{}},
		Step: StepInfo{Index: 2, Name: RoleDo, Dir: "/tmp"},
		Paths: RequestPaths{
			WorkspaceDir: "/tmp",
			RunDir:       "/tmp",
		},
		Budgets: Budgets{
			MaxIterations:      1,
			MaxWallTimeMinutes: 10,
			MaxFailedChecks:    1,
		},
		Context: RequestContext{
			Facts: map[string]any{},
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Do: &DoInput{
			WorkPlan: WorkPlan{
				TimeboxMinutes: 10,
				DoSteps:        []DoStep{},
				CheckSteps:     []CheckStep{},
				StopTriggers:   []string{},
			},
			EffectiveCriteria: []EffectiveAcceptanceCriterion{
				{
					ID:     "AC-1",
					Origin: "baseline",
					Text:   "ok",
					Checks: []Check{
						{ID: "CHK-1", Cmd: "true", ExpectExitCodes: []int{0}},
					},
				},
			},
		},
	}

	mapped, err := role.MapRequest(req)
	require.NoError(t, err)

	data, err := json.Marshal(mapped)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	doInput, ok := payload["do_input"].(map[string]any)
	require.True(t, ok)

	effectiveAny, ok := doInput["acceptance_criteria_effective"].([]any)
	require.True(t, ok)
	require.Len(t, effectiveAny, 1)

	ac, ok := effectiveAny[0].(map[string]any)
	require.True(t, ok)

	refines, ok := ac["refines"].([]any)
	require.True(t, ok, "refines should be an array, not null")
	require.Len(t, refines, 0)
}
