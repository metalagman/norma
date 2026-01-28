package normaloop

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/workflows/normaloop/act"
	"github.com/metalagman/norma/internal/workflows/normaloop/check"
	"github.com/metalagman/norma/internal/workflows/normaloop/common"
	"github.com/metalagman/norma/internal/workflows/normaloop/do"
	"github.com/metalagman/norma/internal/workflows/normaloop/plan"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

// GetInputSchema returns the input schema for the given role.
func GetInputSchema(role string) string {
	switch role {
	case RolePlan:
		return plan.InputSchema
	case RoleDo:
		return do.InputSchema
	case RoleCheck:
		return check.InputSchema
	case RoleAct:
		return act.InputSchema
	default:
		return ""
	}
}

// GetOutputSchema returns the output schema for the given role.
func GetOutputSchema(role string) string {
	switch role {
	case RolePlan:
		return plan.OutputSchema
	case RoleDo:
		return do.OutputSchema
	case RoleCheck:
		return check.OutputSchema
	case RoleAct:
		return act.OutputSchema
	default:
		return ""
	}
}

// AgentPrompt returns the system prompt for a given request and model.
func AgentPrompt(req model.AgentRequest, modelName string) (string, error) {
	data := struct {
		Request   model.AgentRequest
		ModelName string
	}{
		Request:   req,
		ModelName: modelName,
	}

	// 1. Render common base prompt
	baseTmpl, err := template.New("base").Parse(common.BasePromptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse base prompt template: %w", err)
	}
	var baseBuf bytes.Buffer
	if err := baseTmpl.Execute(&baseBuf, data); err != nil {
		return "", fmt.Errorf("execute base prompt template: %w", err)
	}

	// 2. Render role-specific prompt
	var roleTmplStr string
	switch req.Step.Name {
	case RolePlan:
		roleTmplStr = plan.PromptTemplate
	case RoleDo:
		roleTmplStr = do.PromptTemplate
	case RoleCheck:
		roleTmplStr = check.PromptTemplate
	case RoleAct:
		roleTmplStr = act.PromptTemplate
	default:
		return "", fmt.Errorf("unknown role %q", req.Step.Name)
	}

	roleTmpl, err := template.New(req.Step.Name).Parse(roleTmplStr)
	if err != nil {
		return "", fmt.Errorf("parse role prompt template for %q: %w", req.Step.Name, err)
	}
	var roleBuf bytes.Buffer
	if err := roleTmpl.Execute(&roleBuf, data); err != nil {
		return "", fmt.Errorf("execute role prompt template for %q: %w", req.Step.Name, err)
	}

	return baseBuf.String() + "\n" + roleBuf.String(), nil
}