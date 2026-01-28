package normaloop

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/metalagman/norma/internal/model"
	"github.com/metalagman/norma/internal/workflows/normaloop/act"
	"github.com/metalagman/norma/internal/workflows/normaloop/check"
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

//go:embed common.gotmpl
var commonPromptTemplate string

// AgentPrompt returns the system prompt for a given request.
func AgentPrompt(req model.AgentRequest) (string, error) {
	// 1. Render common base prompt
	baseTmpl, err := template.New("base").Parse(commonPromptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse base prompt template: %w", err)
	}
	var baseBuf bytes.Buffer
	if err := baseTmpl.Execute(&baseBuf, struct {
		Request model.AgentRequest
	}{Request: req}); err != nil {
		return "", fmt.Errorf("execute base prompt template: %w", err)
	}

	// 2. Prepare data for role template
	data := struct {
		Request      model.AgentRequest
		CommonPrompt string
	}{
		Request:      req,
		CommonPrompt: baseBuf.String(),
	}

	var tmplStr string
	switch req.Step.Name {
	case RolePlan:
		tmplStr = plan.PromptTemplate
	case RoleDo:
		tmplStr = do.PromptTemplate
	case RoleCheck:
		tmplStr = check.PromptTemplate
	case RoleAct:
		tmplStr = act.PromptTemplate
	default:
		return "", fmt.Errorf("unknown role %q", req.Step.Name)
	}

	tmpl, err := template.New(req.Step.Name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parse prompt template for %q: %w", req.Step.Name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute prompt template for %q: %w", req.Step.Name, err)
	}

	return buf.String(), nil
}
