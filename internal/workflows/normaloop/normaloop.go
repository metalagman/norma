package normaloop

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"
	"text/template"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

// Role defines the interface for a workflow step implementation.
type Role interface {
	Name() string
	InputSchema() string
	OutputSchema() string
	Prompt(req AgentRequest) (string, error)
	MapRequest(req AgentRequest) (any, error)
	MapResponse(outBytes []byte) (AgentResponse, error)
}

var (
	roles    = make(map[string]Role)
	initOnce sync.Once
)

func initializeRoles() {
	initOnce.Do(func() {
		registerDefaultRoles()
	})
}

func mustRegister(r Role) {
	roles[r.Name()] = r
}

// GetRole returns the role implementation by name.
func GetRole(name string) Role {
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

func (r *baseRole) Prompt(req AgentRequest) (string, error) {
	var baseBuf bytes.Buffer
	if err := r.baseTmpl.Execute(&baseBuf, struct {
		Request AgentRequest
	}{Request: req}); err != nil {
		return "", fmt.Errorf("execute base prompt template: %w", err)
	}

	data := struct {
		Request      AgentRequest
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
