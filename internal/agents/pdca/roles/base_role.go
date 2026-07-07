package roles

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/normahq/norma/v2/internal/agents/pdca/contracts"
)

//go:embed common.gotmpl
var commonPromptTemplate string

type baseRole struct {
	name     string
	schemas  contracts.SchemaPair
	baseTmpl *template.Template
	roleTmpl *template.Template
}

func newBaseRole(name, inputSchema, outputSchema, roleTmplStr string) *baseRole {
	baseTmpl := template.Must(template.New(name + "-base").Parse(commonPromptTemplate))
	roleTmpl := template.Must(template.New(name).Parse(roleTmplStr))
	return &baseRole{
		name: name,
		schemas: contracts.SchemaPair{
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		},
		baseTmpl: baseTmpl,
		roleTmpl: roleTmpl,
	}
}

func (r *baseRole) Name() string                  { return r.name }
func (r *baseRole) Schemas() contracts.SchemaPair { return r.schemas }

func (r *baseRole) Prompt(req contracts.RawAgentRequest) (string, error) {
	var baseBuf bytes.Buffer
	if err := r.baseTmpl.Execute(&baseBuf, struct {
		Request contracts.RawAgentRequest
	}{Request: req}); err != nil {
		return "", fmt.Errorf("execute base prompt template: %w", err)
	}

	data := struct {
		Request      contracts.RawAgentRequest
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
