package normaloop

import _ "embed"

//go:embed schemas/input.json
var InputSchema string

//go:embed schemas/output.json
var OutputSchema string

//go:embed schemas/plan.json
var PlanSchema string

//go:embed schemas/do.json
var DoSchema string

//go:embed schemas/check.json
var CheckSchema string

//go:embed schemas/act.json
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
