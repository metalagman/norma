package roles

import (
	"encoding/json"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles/act"
	"github.com/metalagman/norma/internal/agents/pdca/roles/check"
	"github.com/metalagman/norma/internal/agents/pdca/roles/do"
	"github.com/metalagman/norma/internal/agents/pdca/roles/plan"
)

const (
	rolePlan  = "plan"
	roleDo    = "do"
	roleCheck = "check"
	roleAct   = "act"
)

// DefaultRoles returns the built-in PDCA role implementations keyed by role name.
func DefaultRoles() map[string]contracts.Role {
	return map[string]contracts.Role{
		rolePlan:  &planRole{baseRole: *newBaseRole(rolePlan, plan.InputSchema, plan.OutputSchema, plan.PromptTemplate)},
		roleDo:    &doRole{baseRole: *newBaseRole(roleDo, do.InputSchema, do.OutputSchema, do.PromptTemplate)},
		roleCheck: &checkRole{baseRole: *newBaseRole(roleCheck, check.InputSchema, check.OutputSchema, check.PromptTemplate)},
		roleAct:   &actRole{baseRole: *newBaseRole(roleAct, act.InputSchema, act.OutputSchema, act.PromptTemplate)},
	}
}

type planRole struct {
	baseRole
}

//nolint:dupl // Typed generated requests require repeated field mapping.
func (r *planRole) MapRequest(req contracts.AgentRequest) (any, error) {
	acs := make([]plan.PlanAcceptanceCriteria, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		hints := ac.VerifyHints
		if hints == nil {
			hints = []string{}
		}
		acs = append(acs, plan.PlanAcceptanceCriteria{
			Id:          ac.ID,
			Text:        ac.Text,
			VerifyHints: hints,
		})
	}
	links := req.Context.Links
	if links == nil {
		links = []string{}
	}
	return &plan.PlanRequest{
		Run:   &plan.PlanRun{Id: req.Run.ID, Iteration: int64(req.Run.Iteration)},
		Task:  &plan.PlanTask{Id: req.Task.ID, Title: req.Task.Title, Description: req.Task.Description, AcceptanceCriteria: acs},
		Step:  &plan.PlanStep{Index: int64(req.Step.Index), Name: req.Step.Name},
		Paths: &plan.PlanPaths{WorkspaceDir: req.Paths.WorkspaceDir, RunDir: req.Paths.RunDir, Progress: req.Paths.Progress},
		Budgets: &plan.PlanBudgets{
			MaxIterations:      int64(req.Budgets.MaxIterations),
			MaxWallTimeMinutes: int64(req.Budgets.MaxWallTimeMinutes),
			MaxFailedChecks:    int64(req.Budgets.MaxFailedChecks),
		},
		Context: &plan.PlanContext{
			Attempt: int64(req.Context.Attempt),
			Links:   links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		PlanInput:          req.Plan,
	}, nil
}

func (r *planRole) MapResponse(outBytes []byte) (contracts.AgentResponse, error) {
	var roleResp plan.PlanResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.AgentResponse{}, err
	}
	res := contracts.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = contracts.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = contracts.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Plan = roleResp.PlanOutput
	return res, nil
}

type doRole struct {
	baseRole
}

func (r *doRole) MapRequest(req contracts.AgentRequest) (any, error) {
	acs := make([]do.DoAcceptanceCriteria, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		hints := ac.VerifyHints
		if hints == nil {
			hints = []string{}
		}
		acs = append(acs, do.DoAcceptanceCriteria{
			Id:          ac.ID,
			Text:        ac.Text,
			VerifyHints: hints,
		})
	}

	links := req.Context.Links
	if links == nil {
		links = []string{}
	}

	doInput := normalizeDoInput(req.Do)

	return &do.DoRequest{
		Run:   &do.DoRun{Id: req.Run.ID, Iteration: int64(req.Run.Iteration)},
		Task:  &do.DoTask{Id: req.Task.ID, Title: req.Task.Title, Description: req.Task.Description, AcceptanceCriteria: acs},
		Step:  &do.DoStep{Index: int64(req.Step.Index), Name: req.Step.Name},
		Paths: &do.DoPaths{WorkspaceDir: req.Paths.WorkspaceDir, RunDir: req.Paths.RunDir, Progress: req.Paths.Progress},
		Budgets: &do.DoBudgets{
			MaxIterations:      int64(req.Budgets.MaxIterations),
			MaxWallTimeMinutes: int64(req.Budgets.MaxWallTimeMinutes),
			MaxFailedChecks:    int64(req.Budgets.MaxFailedChecks),
		},
		Context: &do.DoContext{
			Attempt: int64(req.Context.Attempt),
			Links:   links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		DoInput:            doInput,
	}, nil
}

func (r *doRole) MapResponse(outBytes []byte) (contracts.AgentResponse, error) {
	var roleResp do.DoResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.AgentResponse{}, err
	}
	res := contracts.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = contracts.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = contracts.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Do = roleResp.DoOutput
	return res, nil
}

type checkRole struct {
	baseRole
}

//nolint:dupl // Typed generated requests require repeated field mapping.
func (r *checkRole) MapRequest(req contracts.AgentRequest) (any, error) {
	acs := make([]check.CheckAcceptanceCriteria, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		acs = append(acs, check.CheckAcceptanceCriteria{
			Id:   ac.ID,
			Text: ac.Text,
		})
	}

	links := req.Context.Links
	if links == nil {
		links = []string{}
	}

	return &check.CheckRequest{
		Run:   &check.CheckRun{Id: req.Run.ID, Iteration: int64(req.Run.Iteration)},
		Task:  &check.CheckTask{Id: req.Task.ID, Title: req.Task.Title, Description: req.Task.Description, AcceptanceCriteria: acs},
		Step:  &check.CheckStep{Index: int64(req.Step.Index), Name: req.Step.Name},
		Paths: &check.CheckPaths{WorkspaceDir: req.Paths.WorkspaceDir, RunDir: req.Paths.RunDir, Progress: req.Paths.Progress},
		Budgets: &check.CheckBudgets{
			MaxIterations:      int64(req.Budgets.MaxIterations),
			MaxWallTimeMinutes: int64(req.Budgets.MaxWallTimeMinutes),
			MaxFailedChecks:    int64(req.Budgets.MaxFailedChecks),
		},
		Context: &check.CheckContext{
			Attempt: int64(req.Context.Attempt),
			Links:   links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		CheckInput:         req.Check,
	}, nil
}

func (r *checkRole) MapResponse(outBytes []byte) (contracts.AgentResponse, error) {
	var roleResp check.CheckResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.AgentResponse{}, err
	}
	res := contracts.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = contracts.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = contracts.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Check = roleResp.CheckOutput
	return res, nil
}

type actRole struct {
	baseRole
}

//nolint:dupl // Typed generated requests require repeated field mapping.
func (r *actRole) MapRequest(req contracts.AgentRequest) (any, error) {
	acs := make([]any, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		acs = append(acs, ac)
	}

	links := req.Context.Links
	if links == nil {
		links = []string{}
	}

	return &act.ActRequest{
		Run:   &act.ActRun{Id: req.Run.ID, Iteration: int64(req.Run.Iteration)},
		Task:  &act.ActTask{Id: req.Task.ID, Title: req.Task.Title, Description: req.Task.Description, AcceptanceCriteria: acs},
		Step:  &act.ActStep{Index: int64(req.Step.Index), Name: req.Step.Name},
		Paths: &act.ActPaths{WorkspaceDir: req.Paths.WorkspaceDir, RunDir: req.Paths.RunDir, Progress: req.Paths.Progress},
		Budgets: &act.ActBudgets{
			MaxIterations:      int64(req.Budgets.MaxIterations),
			MaxWallTimeMinutes: int64(req.Budgets.MaxWallTimeMinutes),
			MaxFailedChecks:    int64(req.Budgets.MaxFailedChecks),
		},
		Context: &act.ActContext{
			Attempt: int64(req.Context.Attempt),
			Links:   links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		ActInput:           req.Act,
	}, nil
}

func (r *actRole) MapResponse(outBytes []byte) (contracts.AgentResponse, error) {
	var roleResp act.ActResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.AgentResponse{}, err
	}
	res := contracts.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = contracts.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = contracts.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Act = roleResp.ActOutput
	return res, nil
}

func normalizeDoInput(input *do.DoInput) *do.DoInput {
	if input == nil {
		return nil
	}

	out := &do.DoInput{
		AcceptanceCriteriaEffective: make([]do.DoEffectiveAcceptanceCriteria, 0, len(input.AcceptanceCriteriaEffective)),
	}

	if input.WorkPlan != nil {
		doSteps := make([]do.DoDoStep, 0, len(input.WorkPlan.DoSteps))
		for _, step := range input.WorkPlan.DoSteps {
			targets := step.TargetsAcceptanceCriteriaIds
			if targets == nil {
				targets = []string{}
			}
			doSteps = append(doSteps, do.DoDoStep{
				Id:                           step.Id,
				TargetsAcceptanceCriteriaIds: targets,
				Text:                         step.Text,
			})
		}

		checkSteps := make([]do.DoCheckStep, 0, len(input.WorkPlan.CheckSteps))
		checkSteps = append(checkSteps, input.WorkPlan.CheckSteps...)

		stopTriggers := input.WorkPlan.StopTriggers
		if stopTriggers == nil {
			stopTriggers = []string{}
		}

		out.WorkPlan = &do.DoWorkPlan{
			TimeboxMinutes: input.WorkPlan.TimeboxMinutes,
			DoSteps:        doSteps,
			CheckSteps:     checkSteps,
			StopTriggers:   stopTriggers,
		}
	}

	for _, ac := range input.AcceptanceCriteriaEffective {
		refines := ac.Refines
		if refines == nil {
			refines = []string{}
		}
		checks := make([]do.DoAcceptanceCriteriaCheck, 0, len(ac.Checks))
		checks = append(checks, ac.Checks...)

		out.AcceptanceCriteriaEffective = append(out.AcceptanceCriteriaEffective, do.DoEffectiveAcceptanceCriteria{
			Id:      ac.Id,
			Origin:  ac.Origin,
			Refines: refines,
			Text:    ac.Text,
			Checks:  checks,
			Reason:  ac.Reason,
		})
	}

	return out
}
