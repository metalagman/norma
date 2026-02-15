package registry

import (
	"encoding/json"

	"github.com/metalagman/norma/internal/workflows/pdca/models"
	"github.com/metalagman/norma/internal/workflows/pdca/roles/act"
	"github.com/metalagman/norma/internal/workflows/pdca/roles/check"
	"github.com/metalagman/norma/internal/workflows/pdca/roles/do"
	"github.com/metalagman/norma/internal/workflows/pdca/roles/plan"
)

const (
	rolePlan  = "plan"
	roleDo    = "do"
	roleCheck = "check"
	roleAct   = "act"
)

// DefaultRoles returns the built-in PDCA role implementations keyed by role name.
func DefaultRoles() map[string]models.Role {
	return map[string]models.Role{
		rolePlan:  &planRole{baseRole: *newBaseRole(rolePlan, plan.InputSchema, plan.OutputSchema, plan.PromptTemplate)},
		roleDo:    &doRole{baseRole: *newBaseRole(roleDo, do.InputSchema, do.OutputSchema, do.PromptTemplate)},
		roleCheck: &checkRole{baseRole: *newBaseRole(roleCheck, check.InputSchema, check.OutputSchema, check.PromptTemplate)},
		roleAct:   &actRole{baseRole: *newBaseRole(roleAct, act.InputSchema, act.OutputSchema, act.PromptTemplate)},
	}
}

type planRole struct {
	baseRole
}

func (r *planRole) MapRequest(req models.AgentRequest) (any, error) {
	acs := make([]plan.PlanAcceptanceCriterion, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		hints := ac.VerifyHints
		if hints == nil {
			hints = []string{}
		}
		acs = append(acs, plan.PlanAcceptanceCriterion{
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
		PlanInput: &plan.PlanInput{
			Task: &plan.PlanTaskID{Id: req.Plan.Task.ID},
		},
	}, nil
}

func (r *planRole) MapResponse(outBytes []byte) (models.AgentResponse, error) {
	var roleResp plan.PlanResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return models.AgentResponse{}, err
	}
	res := models.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Plan = roleResp.PlanOutput
	return res, nil
}

type doRole struct {
	baseRole
}

func (r *doRole) MapRequest(req models.AgentRequest) (any, error) {
	acs := make([]do.DoAcceptanceCriterion, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		hints := ac.VerifyHints
		if hints == nil {
			hints = []string{}
		}
		acs = append(acs, do.DoAcceptanceCriterion{
			Id:          ac.ID,
			Text:        ac.Text,
			VerifyHints: hints,
		})
	}
	effective := make([]do.DoEffectiveAC, 0, len(req.Do.EffectiveCriteria))
	for _, ac := range req.Do.EffectiveCriteria {
		refines := ac.Refines
		if refines == nil {
			refines = []string{}
		}
		checks := make([]do.DoACCheck, 0, len(ac.Checks))
		for _, c := range ac.Checks {
			checks = append(checks, do.DoACCheck{
				Id:              c.Id,
				Cmd:             c.Cmd,
				ExpectExitCodes: c.ExpectExitCodes,
			})
		}
		effective = append(effective, do.DoEffectiveAC{
			Id:      ac.Id,
			Origin:  ac.Origin,
			Refines: refines,
			Text:    ac.Text,
			Checks:  checks,
			Reason:  ac.Reason,
		})
	}

	doSteps := make([]do.DoDoStep, 0, len(req.Do.WorkPlan.DoSteps))
	for _, s := range req.Do.WorkPlan.DoSteps {
		targetsACIDs := s.TargetsAcIds
		if targetsACIDs == nil {
			targetsACIDs = []string{}
		}
		doSteps = append(doSteps, do.DoDoStep{
			Id:           s.Id,
			Text:         s.Text,
			TargetsAcIds: targetsACIDs,
		})
	}

	checkSteps := make([]do.DoCheckStep, 0, len(req.Do.WorkPlan.CheckSteps))
	for _, s := range req.Do.WorkPlan.CheckSteps {
		checkSteps = append(checkSteps, do.DoCheckStep{
			Id:   s.Id,
			Text: s.Text,
			Mode: s.Mode,
		})
	}

	stopTriggers := req.Do.WorkPlan.StopTriggers
	if stopTriggers == nil {
		stopTriggers = []string{}
	}

	links := req.Context.Links
	if links == nil {
		links = []string{}
	}

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
		DoInput: &do.DoInput{
			WorkPlan: &do.DoWorkPlan{
				TimeboxMinutes: req.Do.WorkPlan.TimeboxMinutes,
				DoSteps:        doSteps,
				CheckSteps:     checkSteps,
				StopTriggers:   stopTriggers,
			},
			AcceptanceCriteriaEffective: effective,
		},
	}, nil
}

func (r *doRole) MapResponse(outBytes []byte) (models.AgentResponse, error) {
	var roleResp do.DoResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return models.AgentResponse{}, err
	}
	res := models.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Do = roleResp.DoOutput
	return res, nil
}

type checkRole struct {
	baseRole
}

func (r *checkRole) MapRequest(req models.AgentRequest) (any, error) {
	acs := make([]check.CheckAcceptanceCriterion, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		acs = append(acs, check.CheckAcceptanceCriterion{
			Id:   ac.ID,
			Text: ac.Text,
		})
	}
	effective := make([]check.CheckEffectiveAC, 0, len(req.Check.EffectiveCriteria))
	for _, ac := range req.Check.EffectiveCriteria {
		effective = append(effective, check.CheckEffectiveAC{
			Id:     ac.Id,
			Origin: ac.Origin,
			Text:   ac.Text,
		})
	}
	doSteps := make([]check.CheckDoStep, 0, len(req.Check.WorkPlan.DoSteps))
	for _, s := range req.Check.WorkPlan.DoSteps {
		doSteps = append(doSteps, check.CheckDoStep{
			Id:   s.Id,
			Text: s.Text,
		})
	}
	checkSteps := make([]check.CheckCheckStep, 0, len(req.Check.WorkPlan.CheckSteps))
	for _, s := range req.Check.WorkPlan.CheckSteps {
		checkSteps = append(checkSteps, check.CheckCheckStep{
			Id:   s.Id,
			Text: s.Text,
			Mode: s.Mode,
		})
	}

	stopTriggers := req.Check.WorkPlan.StopTriggers
	if stopTriggers == nil {
		stopTriggers = []string{}
	}

	executedStepIDs := req.Check.DoExecution.ExecutedStepIds
	if executedStepIDs == nil {
		executedStepIDs = []string{}
	}

	skippedStepIDs := req.Check.DoExecution.SkippedStepIds
	if skippedStepIDs == nil {
		skippedStepIDs = []string{}
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
		CheckInput: &check.CheckInput{
			WorkPlan: &check.CheckWorkPlan{
				TimeboxMinutes: req.Check.WorkPlan.TimeboxMinutes,
				DoSteps:        doSteps,
				CheckSteps:     checkSteps,
				StopTriggers:   stopTriggers,
			},
			AcceptanceCriteriaEffective: effective,
			DoExecution: &check.CheckDoExecution{
				ExecutedStepIds: executedStepIDs,
				SkippedStepIds:  skippedStepIDs,
			},
		},
	}, nil
}

func (r *checkRole) MapResponse(outBytes []byte) (models.AgentResponse, error) {
	var roleResp check.CheckResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return models.AgentResponse{}, err
	}
	res := models.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Check = roleResp.CheckOutput
	return res, nil
}

type actRole struct {
	baseRole
}

func (r *actRole) MapRequest(req models.AgentRequest) (any, error) {
	acs := make([]any, 0, len(req.Task.AcceptanceCriteria))
	for _, ac := range req.Task.AcceptanceCriteria {
		acs = append(acs, ac)
	}
	acceptanceResults := make([]act.ActAcceptanceResult, 0, len(req.Act.AcceptanceResults))
	for _, ar := range req.Act.AcceptanceResults {
		acceptanceResults = append(acceptanceResults, act.ActAcceptanceResult{
			AcId:   ar.AcId,
			Result: ar.Result,
			Notes:  ar.Notes,
		})
	}

	links := req.Context.Links
	if links == nil {
		links = []string{}
	}

	actReq := &act.ActRequest{
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
		ActInput: &act.ActInput{
			CheckVerdict: &act.ActCheckVerdict{
				Status:         req.Act.CheckVerdict.Status,
				Recommendation: req.Act.CheckVerdict.Recommendation,
			},
			AcceptanceResults: acceptanceResults,
		},
	}
	if req.Act.CheckVerdict.Basis != nil {
		actReq.ActInput.CheckVerdict.Basis = &act.ActCheckVerdictBasis{
			PlanMatch:           req.Act.CheckVerdict.Basis.PlanMatch,
			AllAcceptancePassed: req.Act.CheckVerdict.Basis.AllAcceptancePassed,
		}
	}
	return actReq, nil
}

func (r *actRole) MapResponse(outBytes []byte) (models.AgentResponse, error) {
	var roleResp act.ActResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return models.AgentResponse{}, err
	}
	res := models.AgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	if roleResp.Summary != nil {
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	res.Act = roleResp.ActOutput
	return res, nil
}
