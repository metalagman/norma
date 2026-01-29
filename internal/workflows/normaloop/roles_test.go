package normaloop

import (
	"encoding/json"
	"testing"

	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/stretchr/testify/require"
)

func TestDoRoleMapRequestRefinesDefaultsToEmptySlice(t *testing.T) {
	role := &doRole{}

	req := models.AgentRequest{
		Run:  models.RunInfo{ID: "run-1", Iteration: 1},
		Task: models.TaskInfo{ID: "task-1", Title: "title", Description: "desc", AcceptanceCriteria: []task.AcceptanceCriterion{}},
		Step: models.StepInfo{Index: 2, Name: RoleDo},
		Paths: models.RequestPaths{
			WorkspaceDir: "/tmp",
			RunDir:       "/tmp",
		},
		Budgets: models.Budgets{
			MaxIterations:      1,
			MaxWallTimeMinutes: 10,
			MaxFailedChecks:    1,
		},
		Context: models.RequestContext{
			Facts: map[string]any{},
			Links: []string{},
		},
		StopReasonsAllowed: []string{"budget_exceeded"},
		Do: &models.DoInput{
			WorkPlan: models.WorkPlan{
				TimeboxMinutes: 10,
				DoSteps:        []models.DoStep{},
				CheckSteps:     []models.CheckStep{},
				StopTriggers:   []string{},
			},
			EffectiveCriteria: []models.EffectiveAcceptanceCriterion{
				{
					ID:     "AC-1",
					Origin: "baseline",
					Text:   "ok",
					Checks: []models.Check{
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
