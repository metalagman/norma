package normaloop

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"github.com/metalagman/norma/internal/model"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

//go:embed prompts/*.gotmpl
var promptFS embed.FS

var prompts = template.Must(template.ParseFS(promptFS, "prompts/*.gotmpl"))

// AgentPrompt returns the system prompt for a given request and model.
func AgentPrompt(req model.AgentRequest, modelName string) (string, error) {
	data := struct {
		Request   model.AgentRequest
		ModelName string
	}{
		Request:   req,
		ModelName: modelName,
	}

	var buf bytes.Buffer
	templateName := fmt.Sprintf("%s.gotmpl", req.Step.Name)
	if err := prompts.ExecuteTemplate(&buf, templateName, data); err != nil {
		return "", fmt.Errorf("execute prompt template %q: %w", templateName, err)
	}

	return buf.String(), nil
}