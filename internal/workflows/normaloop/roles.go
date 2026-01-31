package normaloop

import (
	"encoding/json"

	"github.com/metalagman/norma/internal/task"
	"github.com/metalagman/norma/internal/workflows/normaloop/act"
	"github.com/metalagman/norma/internal/workflows/normaloop/check"
	"github.com/metalagman/norma/internal/workflows/normaloop/do"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/metalagman/norma/internal/workflows/normaloop/plan"
)

func registerDefaultRoles() {
	mustRegister(&planRole{baseRole: *newBaseRole(RolePlan, plan.InputSchema, plan.OutputSchema, plan.PromptTemplate)})
	mustRegister(&doRole{baseRole: *newBaseRole(RoleDo, do.InputSchema, do.OutputSchema, do.PromptTemplate)})
	mustRegister(&checkRole{baseRole: *newBaseRole(RoleCheck, check.InputSchema, check.OutputSchema, check.PromptTemplate)})
	mustRegister(&actRole{baseRole: *newBaseRole(RoleAct, act.InputSchema, act.OutputSchema, act.PromptTemplate)})
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
			Links:   req.Context.Links,
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
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text, Warnings: roleResp.Summary.Warnings, Errors: roleResp.Summary.Errors}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	if roleResp.PlanOutput != nil {
		res.Plan = &models.PlanOutput{
			TaskID:      roleResp.PlanOutput.TaskId,
			Goal:        roleResp.PlanOutput.Goal,
			Constraints: roleResp.PlanOutput.Constraints,
		}
		if roleResp.PlanOutput.WorkPlan != nil {
			res.Plan.WorkPlan = models.WorkPlan{
				TimeboxMinutes: int(roleResp.PlanOutput.WorkPlan.TimeboxMinutes),
				StopTriggers:   roleResp.PlanOutput.WorkPlan.StopTriggers,
			}
			for _, s := range roleResp.PlanOutput.WorkPlan.DoSteps {
				res.Plan.WorkPlan.DoSteps = append(res.Plan.WorkPlan.DoSteps, models.DoStep{
					ID:   s.Id,
					Text: s.Text,
				})
			}
			for _, s := range roleResp.PlanOutput.WorkPlan.CheckSteps {
				res.Plan.WorkPlan.CheckSteps = append(res.Plan.WorkPlan.CheckSteps, models.CheckStep{
					ID:   s.Id,
					Text: s.Text,
					Mode: s.Mode,
				})
			}
		}
		if roleResp.PlanOutput.AcceptanceCriteria != nil {
			for _, b := range roleResp.PlanOutput.AcceptanceCriteria.Baseline {
				res.Plan.AcceptanceCriteria.Baseline = append(res.Plan.AcceptanceCriteria.Baseline, task.AcceptanceCriterion{
					ID:   b.Id,
					Text: b.Text,
				})
			}
			for _, e := range roleResp.PlanOutput.AcceptanceCriteria.Effective {
				res.Plan.AcceptanceCriteria.Effective = append(res.Plan.AcceptanceCriteria.Effective, models.EffectiveAcceptanceCriterion{
					ID:     e.Id,
					Origin: e.Origin,
					Text:   e.Text,
				})
			}
		}
	}
	if roleResp.Timing != nil {
		res.Timing = models.ResponseTiming{WallTimeMS: roleResp.Timing.WallTimeMs}
	}
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
			exitCodes := make([]int64, 0, len(c.ExpectExitCodes))
			for _, ec := range c.ExpectExitCodes {
				exitCodes = append(exitCodes, int64(ec))
			}
			checks = append(checks, do.DoACCheck{
				Id:              c.ID,
				Cmd:             c.Cmd,
				ExpectExitCodes: exitCodes,
			})
		}
		effective = append(effective, do.DoEffectiveAC{
			Id:      ac.ID,
			Origin:  ac.Origin,
			Refines: refines,
			Text:    ac.Text,
			Checks:  checks,
			Reason:  ac.Reason,
		})
	}

	doSteps := make([]do.DoDoStep, 0, len(req.Do.WorkPlan.DoSteps))
	for _, s := range req.Do.WorkPlan.DoSteps {
		commands := make([]do.DoCommand, 0, len(s.Commands))
		for _, c := range s.Commands {
			exitCodes := make([]int64, 0, len(c.ExpectExitCodes))
			for _, ec := range c.ExpectExitCodes {
				exitCodes = append(exitCodes, int64(ec))
			}
			commands = append(commands, do.DoCommand{
				Id:              c.ID,
				Cmd:             c.Cmd,
				ExpectExitCodes: exitCodes,
			})
		}
		doSteps = append(doSteps, do.DoDoStep{
			Id:           s.ID,
			Text:         s.Text,
			Commands:     commands,
			TargetsAcIds: s.TargetsACIDs,
		})
	}

	checkSteps := make([]do.DoCheckStep, 0, len(req.Do.WorkPlan.CheckSteps))
	for _, s := range req.Do.WorkPlan.CheckSteps {
		checkSteps = append(checkSteps, do.DoCheckStep{
			Id:   s.ID,
			Text: s.Text,
			Mode: s.Mode,
		})
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
			Links:   req.Context.Links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		DoInput: &do.DoInput{
			WorkPlan: &do.DoWorkPlan{
				TimeboxMinutes: int64(req.Do.WorkPlan.TimeboxMinutes),
				DoSteps:        doSteps,
				CheckSteps:     checkSteps,
				StopTriggers:   req.Do.WorkPlan.StopTriggers,
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
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text, Warnings: roleResp.Summary.Warnings, Errors: roleResp.Summary.Errors}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	if roleResp.DoOutput != nil {
		res.Do = &models.DoOutput{}
		if roleResp.DoOutput.Execution != nil {
			res.Do.Execution = models.DoExecution{
				ExecutedStepIDs: roleResp.DoOutput.Execution.ExecutedStepIds,
				SkippedStepIDs:  roleResp.DoOutput.Execution.SkippedStepIds,
			}
			for _, c := range roleResp.DoOutput.Execution.Commands {
				res.Do.Execution.Commands = append(res.Do.Execution.Commands, models.CommandResult{
					ID:       c.Id,
					Cmd:      c.Cmd,
					ExitCode: int(c.ExitCode),
				})
			}
		}
		for _, b := range roleResp.DoOutput.Blockers {
			res.Do.Blockers = append(res.Do.Blockers, models.Blocker{
				Kind:                b.Kind,
				Text:                b.Text,
				SuggestedStopReason: b.SuggestedStopReason,
			})
		}
	}
	if roleResp.Timing != nil {
		res.Timing = models.ResponseTiming{WallTimeMS: roleResp.Timing.WallTimeMs}
	}
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
			Id:     ac.ID,
			Origin: ac.Origin,
			Text:   ac.Text,
		})
	}
	commands := make([]check.CheckCommandResult, 0, len(req.Check.DoExecution.Commands))
	for _, c := range req.Check.DoExecution.Commands {
		commands = append(commands, check.CheckCommandResult{
			Id:       c.ID,
			Cmd:      c.Cmd,
			ExitCode: int64(c.ExitCode),
		})
	}
	doSteps := make([]check.CheckDoStep, 0, len(req.Check.WorkPlan.DoSteps))
	for _, s := range req.Check.WorkPlan.DoSteps {
		doSteps = append(doSteps, check.CheckDoStep{
			Id:   s.ID,
			Text: s.Text,
		})
	}
	checkSteps := make([]check.CheckCheckStep, 0, len(req.Check.WorkPlan.CheckSteps))
	for _, s := range req.Check.WorkPlan.CheckSteps {
		checkSteps = append(checkSteps, check.CheckCheckStep{
			Id:   s.ID,
			Text: s.Text,
			Mode: s.Mode,
		})
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
			Links:   req.Context.Links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		CheckInput: &check.CheckInput{
			WorkPlan: &check.CheckWorkPlan{
				TimeboxMinutes: int64(req.Check.WorkPlan.TimeboxMinutes),
				DoSteps:        doSteps,
				CheckSteps:     checkSteps,
				StopTriggers:   req.Check.WorkPlan.StopTriggers,
			},
			AcceptanceCriteriaEffective: effective,
			DoExecution: &check.CheckDoExecution{
				ExecutedStepIds: req.Check.DoExecution.ExecutedStepIDs,
				SkippedStepIds:  req.Check.DoExecution.SkippedStepIDs,
				Commands:        commands,
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
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text, Warnings: roleResp.Summary.Warnings, Errors: roleResp.Summary.Errors}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	if roleResp.CheckOutput != nil {
		res.Check = &models.CheckOutput{}
		if roleResp.CheckOutput.PlanMatch != nil {
			if roleResp.CheckOutput.PlanMatch.DoSteps != nil {
				res.Check.PlanMatch.DoSteps = models.MatchResult{
					PlannedIDs:    roleResp.CheckOutput.PlanMatch.DoSteps.PlannedIds,
					ExecutedIDs:   roleResp.CheckOutput.PlanMatch.DoSteps.ExecutedIds,
					MissingIDs:    roleResp.CheckOutput.PlanMatch.DoSteps.MissingIds,
					UnexpectedIDs: roleResp.CheckOutput.PlanMatch.DoSteps.UnexpectedIds,
				}
			}
			if roleResp.CheckOutput.PlanMatch.Commands != nil {
				res.Check.PlanMatch.Commands = models.MatchResult{
					PlannedIDs:    roleResp.CheckOutput.PlanMatch.Commands.PlannedIds,
					ExecutedIDs:   roleResp.CheckOutput.PlanMatch.Commands.ExecutedIds,
					MissingIDs:    roleResp.CheckOutput.PlanMatch.Commands.MissingIds,
					UnexpectedIDs: roleResp.CheckOutput.PlanMatch.Commands.UnexpectedIds,
				}
			}
		}
		if roleResp.CheckOutput.Verdict != nil {
			res.Check.Verdict = models.CheckVerdict{
				Status:         roleResp.CheckOutput.Verdict.Status,
				Recommendation: roleResp.CheckOutput.Verdict.Recommendation,
			}
			if roleResp.CheckOutput.Verdict.Basis != nil {
				res.Check.Verdict.Basis = models.Basis{
					PlanMatch:           roleResp.CheckOutput.Verdict.Basis.PlanMatch,
					AllAcceptancePassed: roleResp.CheckOutput.Verdict.Basis.AllAcceptancePassed,
				}
			}
		}
		for _, ar := range roleResp.CheckOutput.AcceptanceResults {
			res.Check.AcceptanceResults = append(res.Check.AcceptanceResults, models.AcceptanceResult{
				ACID:   ar.AcId,
				Result: ar.Result,
				Notes:  ar.Notes,
			})
		}
		for _, n := range roleResp.CheckOutput.ProcessNotes {
			res.Check.ProcessNotes = append(res.Check.ProcessNotes, models.ProcessNote{
				Kind:                n.Kind,
				Severity:            n.Severity,
				Text:                n.Text,
				SuggestedStopReason: n.SuggestedStopReason,
			})
		}
	}
	if roleResp.Timing != nil {
		res.Timing = models.ResponseTiming{WallTimeMS: roleResp.Timing.WallTimeMs}
	}
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
			AcId:   ar.ACID,
			Result: ar.Result,
			Notes:  ar.Notes,
		})
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
			Links:   req.Context.Links,
		},
		StopReasonsAllowed: req.StopReasonsAllowed,
		ActInput: &act.ActInput{
			CheckVerdict: &act.ActCheckVerdict{
				Status:         req.Act.CheckVerdict.Status,
				Recommendation: req.Act.CheckVerdict.Recommendation,
				Basis: &act.ActCheckVerdictBasis{
					PlanMatch:           req.Act.CheckVerdict.Basis.PlanMatch,
					AllAcceptancePassed: req.Act.CheckVerdict.Basis.AllAcceptancePassed,
				},
			},
			AcceptanceResults: acceptanceResults,
		},
	}, nil
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
		res.Summary = models.ResponseSummary{Text: roleResp.Summary.Text, Warnings: roleResp.Summary.Warnings, Errors: roleResp.Summary.Errors}
	}
	if roleResp.Progress != nil {
		res.Progress = models.StepProgress{Title: roleResp.Progress.Title, Details: roleResp.Progress.Details}
	}
	if roleResp.ActOutput != nil {
		res.Act = &models.ActOutput{
			Decision:  roleResp.ActOutput.Decision,
			Rationale: roleResp.ActOutput.Rationale,
		}
		if roleResp.ActOutput.Next != nil {
			res.Act.Next = models.NextAction{
				Recommended: roleResp.ActOutput.Next.Recommended,
				Notes:       roleResp.ActOutput.Next.Notes,
			}
		}
	}
	if roleResp.Timing != nil {
		res.Timing = models.ResponseTiming{WallTimeMS: roleResp.Timing.WallTimeMs}
	}
	return res, nil
}
