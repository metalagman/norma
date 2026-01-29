package normaloop

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"
	"text/template"

	"github.com/metalagman/norma/internal/workflows/normaloop/models"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

var (
	roles    = make(map[string]models.Role)
	initOnce sync.Once
)

func initializeRoles() {
	initOnce.Do(func() {
		registerDefaultRoles()
	})
}

func mustRegister(r models.Role) {
	roles[r.Name()] = r
}

// GetRole returns the role implementation by name.
func GetRole(name string) models.Role {
	initializeRoles()
	return roles[name]
}

//go:embed common.gotmpl
var commonPromptTemplate string

type baseRole struct {
	name         string
	inputSchema  string
	outputSchema string
	baseTmpl     *template.Template
	roleTmpl     *template.Template
	runner       any
}

func newBaseRole(name, inputSchema, outputSchema, roleTmplStr string) *baseRole {
	baseTmpl := template.Must(template.New(name + "-base").Parse(commonPromptTemplate))
	roleTmpl := template.Must(template.New(name).Parse(roleTmplStr))
	return &baseRole{
		name:         name,
		inputSchema:  inputSchema,
		outputSchema: outputSchema,
		baseTmpl:     baseTmpl,
		roleTmpl:     roleTmpl,
	}
}

func (r *baseRole) Name() string         { return r.name }
func (r *baseRole) InputSchema() string  { return r.inputSchema }
func (r *baseRole) OutputSchema() string { return r.outputSchema }
func (r *baseRole) SetRunner(runner any) { r.runner = runner }
func (r *baseRole) Runner() any          { return r.runner }

func (r *baseRole) Prompt(req models.AgentRequest) (string, error) {
	var baseBuf bytes.Buffer
	if err := r.baseTmpl.Execute(&baseBuf, struct {
		Request models.AgentRequest
	}{Request: req}); err != nil {
		return "", fmt.Errorf("execute base prompt template: %w", err)
	}

	data := struct {
		Request      models.AgentRequest
		CommonPrompt string
	}{
		Request:      req,
		CommonPrompt: baseBuf.String(),
	}

	var buf bytes.Buffer
	if err := r.roleTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute prompt template: %w", err)
	}

	return buf.String(), nil
}
