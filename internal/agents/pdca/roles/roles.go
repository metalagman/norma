package roles

import (
	"encoding/json"
	"fmt"

	"github.com/normahq/norma/v2/internal/agents/pdca/contracts"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/act"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/check"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/do"
	"github.com/normahq/norma/v2/internal/agents/pdca/roles/plan"
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

func (r *planRole) MapRequest(req contracts.RawAgentRequest) (any, error) {
	var contractReq contracts.AgentRequest
	if err := json.Unmarshal(req, &contractReq); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	acs := make([]plan.BaselineAcceptanceCriterion, 0, len(contractReq.Task.AcceptanceCriteria))
	for _, ac := range contractReq.Task.AcceptanceCriteria {
		hints := ac.VerifyHints
		if hints == nil {
			hints = []string{}
		}
		acs = append(acs, plan.BaselineAcceptanceCriterion{
			Id:          ac.ID,
			Text:        ac.Text,
			VerifyHints: hints,
		})
	}

	return &plan.PlanRequest{
		Run:   &plan.Run{Id: contractReq.Run.ID, Iteration: int64(contractReq.Run.Iteration)},
		Task:  &plan.Task{Id: contractReq.Task.ID, Goal: contractReq.Task.Goal, AcceptanceCriteria: acs},
		Step:  &plan.Step{Index: int64(contractReq.Step.Index)},
		Paths: &plan.Paths{WorkspaceDir: contractReq.Paths.WorkspaceDir},
	}, nil
}

func (r *planRole) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var roleResp plan.PlanResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.RawAgentResponse{}, err
	}
	res := contracts.RawAgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	res.Summary = roleResp.Summary
	if roleResp.PlanOutput != nil {
		if planBytes, err := json.Marshal(roleResp.PlanOutput); err == nil {
			res.PlanOutput = planBytes
		}
	}
	return res, nil
}

type doRole struct {
	baseRole
}

//nolint:dupl // Role request mappers intentionally build parallel generated request types.
func (r *doRole) MapRequest(req contracts.RawAgentRequest) (any, error) {
	var contractReq contracts.AgentRequest
	if err := json.Unmarshal(req, &contractReq); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Do reads plan from the live run's ephemeral TaskState.
	var doInput *do.DoInput
	if len(contractReq.TaskState.Plan) > 0 {
		var planOutput plan.PlanOutput
		if err := json.Unmarshal(contractReq.TaskState.Plan, &planOutput); err != nil {
			return nil, fmt.Errorf("unmarshal plan from task state: %w", err)
		}
		doInput = planOutputToDoInput(&planOutput)
	} else {
		return nil, fmt.Errorf("missing plan in task state for do step")
	}

	return &do.DoRequest{
		Run:     &do.Run{Id: contractReq.Run.ID, Iteration: int64(contractReq.Run.Iteration)},
		Task:    &do.Task{Id: contractReq.Task.ID, Goal: contractReq.Task.Goal},
		Step:    &do.Step{Index: int64(contractReq.Step.Index)},
		Paths:   &do.Paths{WorkspaceDir: contractReq.Paths.WorkspaceDir},
		DoInput: doInput,
	}, nil
}

func (r *doRole) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var roleResp do.DoResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.RawAgentResponse{}, err
	}
	res := contracts.RawAgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	res.Summary = roleResp.Summary
	if roleResp.DoOutput != nil {
		if doBytes, err := json.Marshal(roleResp.DoOutput); err == nil {
			res.DoOutput = doBytes
		}
	}
	return res, nil
}

type checkRole struct {
	baseRole
}

func (r *checkRole) MapRequest(req contracts.RawAgentRequest) (any, error) {
	var contractReq contracts.AgentRequest
	if err := json.Unmarshal(req, &contractReq); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Check reads plan and do from the live run's ephemeral TaskState.
	var checkInput *check.CheckInput
	if len(contractReq.TaskState.Plan) > 0 && len(contractReq.TaskState.Do) > 0 {
		var planOutput plan.PlanOutput
		if err := json.Unmarshal(contractReq.TaskState.Plan, &planOutput); err != nil {
			return nil, fmt.Errorf("unmarshal plan from task state: %w", err)
		}
		var doOutput do.DoOutput
		if err := json.Unmarshal(contractReq.TaskState.Do, &doOutput); err != nil {
			return nil, fmt.Errorf("unmarshal do from task state: %w", err)
		}
		checkInput = planAndDoToCheckInput(&planOutput, &doOutput)
	} else {
		return nil, fmt.Errorf("missing plan or do in task state for check step")
	}

	return &check.CheckRequest{
		Run:        &check.Run{Id: contractReq.Run.ID, Iteration: int64(contractReq.Run.Iteration)},
		Task:       &check.Task{Id: contractReq.Task.ID, Goal: contractReq.Task.Goal},
		Step:       &check.Step{Index: int64(contractReq.Step.Index)},
		Paths:      &check.Paths{WorkspaceDir: contractReq.Paths.WorkspaceDir},
		CheckInput: checkInput,
	}, nil
}

func (r *checkRole) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var roleResp check.CheckResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.RawAgentResponse{}, err
	}
	res := contracts.RawAgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	res.Summary = roleResp.Summary
	if roleResp.CheckOutput != nil {
		if checkBytes, err := json.Marshal(roleResp.CheckOutput); err == nil {
			res.CheckOutput = checkBytes
		}
	}
	return res, nil
}

type actRole struct {
	baseRole
}

//nolint:dupl // Role request mappers intentionally build parallel generated request types.
func (r *actRole) MapRequest(req contracts.RawAgentRequest) (any, error) {
	var contractReq contracts.AgentRequest
	if err := json.Unmarshal(req, &contractReq); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	// Act reads check from the live run's ephemeral TaskState.
	var actInput *act.ActInput
	if len(contractReq.TaskState.Check) > 0 {
		var checkOutput check.CheckOutput
		if err := json.Unmarshal(contractReq.TaskState.Check, &checkOutput); err != nil {
			return nil, fmt.Errorf("unmarshal check from task state: %w", err)
		}
		actInput = checkOutputToActInput(&checkOutput)
	} else {
		return nil, fmt.Errorf("missing check in task state for act step")
	}

	return &act.ActRequest{
		Run:      &act.Run{Id: contractReq.Run.ID, Iteration: int64(contractReq.Run.Iteration)},
		Task:     &act.Task{Id: contractReq.Task.ID, Goal: contractReq.Task.Goal},
		Step:     &act.Step{Index: int64(contractReq.Step.Index)},
		Paths:    &act.Paths{WorkspaceDir: contractReq.Paths.WorkspaceDir},
		ActInput: actInput,
	}, nil
}

func (r *actRole) MapResponse(outBytes []byte) (contracts.RawAgentResponse, error) {
	var roleResp act.ActResponse
	if err := json.Unmarshal(outBytes, &roleResp); err != nil {
		return contracts.RawAgentResponse{}, err
	}
	res := contracts.RawAgentResponse{
		Status:     roleResp.Status,
		StopReason: roleResp.StopReason,
	}
	res.Summary = roleResp.Summary
	if roleResp.ActOutput != nil {
		if actBytes, err := json.Marshal(roleResp.ActOutput); err == nil {
			res.ActOutput = actBytes
		}
	}
	return res, nil
}

// Type conversion helpers - each role converts from its own types when reading ephemeral TaskState.

func planOutputToDoInput(p *plan.PlanOutput) *do.DoInput {
	if p == nil {
		return nil
	}

	doInput := &do.DoInput{
		AcceptanceCriteria: make([]do.AcceptanceCriterion, 0),
		DoSteps:            make([]do.DoStep, 0),
	}

	for _, step := range p.DoSteps {
		doInput.DoSteps = append(doInput.DoSteps, do.DoStep{
			Id:   step.Id,
			Text: step.Text,
		})
	}

	for _, ac := range p.AcceptanceCriteria {
		doInput.AcceptanceCriteria = append(doInput.AcceptanceCriteria, do.AcceptanceCriterion{
			Id:   ac.Id,
			Text: ac.Text,
		})
	}

	return doInput
}

func planAndDoToCheckInput(p *plan.PlanOutput, d *do.DoOutput) *check.CheckInput {
	input := &check.CheckInput{}

	if p != nil {
		input.DoSteps = make([]check.DoStep, 0, len(p.DoSteps))
		for _, step := range p.DoSteps {
			input.DoSteps = append(input.DoSteps, check.DoStep{Id: step.Id, Text: step.Text})
		}
	}

	if p != nil {
		criteria := make([]check.AcceptanceCriterion, 0, len(p.AcceptanceCriteria))
		for _, ac := range p.AcceptanceCriteria {
			checks := make([]check.Check, 0, len(ac.Checks))
			for _, c := range ac.Checks {
				checks = append(checks, check.Check{
					Id:                c.Id,
					Command:           c.Command,
					ExpectedExitCodes: c.ExpectedExitCodes,
				})
			}
			criteria = append(criteria, check.AcceptanceCriterion{
				Id:     ac.Id,
				Text:   ac.Text,
				Checks: checks,
			})
		}
		input.AcceptanceCriteria = criteria
	}

	if d != nil {
		input.ExecutedStepIds = d.ExecutedStepIds
	}

	return input
}

func checkOutputToActInput(c *check.CheckOutput) *act.ActInput {
	if c == nil {
		return nil
	}

	input := &act.ActInput{}

	input.Verdict = c.Verdict

	if c.AcceptanceResults != nil {
		input.AcceptanceResults = make([]act.AcceptanceResult, 0, len(c.AcceptanceResults))
		for _, ar := range c.AcceptanceResults {
			input.AcceptanceResults = append(input.AcceptanceResults, act.AcceptanceResult{
				AcId:   ar.AcId,
				Result: ar.Result,
				Notes:  ar.Notes,
			})
		}
	}

	return input
}
