package normaloop

import _ "embed"

//go:embed schemas/input.schema.json
var InputSchema string

//go:embed schemas/output.schema.json
var OutputSchema string

//go:embed schemas/plan.schema.json
var PlanSchema string

//go:embed schemas/do.schema.json
var DoSchema string

//go:embed schemas/check.schema.json
var CheckSchema string

//go:embed schemas/act.schema.json
var ActSchema string

// GetInputSchema returns the input schema for the given role.
func GetInputSchema(role string) string {
	return InputSchema
}

// GetOutputSchema returns the output schema for the given role.
func GetOutputSchema(role string) string {
	switch role {
	case RolePlan:
		return PlanSchema
	case RoleDo:
		return DoSchema
	case RoleCheck:
		return CheckSchema
	case RoleAct:
		return ActSchema
	default:
		return OutputSchema
	}
}