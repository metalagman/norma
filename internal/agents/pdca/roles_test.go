package pdca

import (
	"encoding/json"
	"testing"

	"github.com/normahq/norma/internal/agents/pdca/contracts"
	"github.com/xeipuuv/gojsonschema"
)

func TestDoRoleMapRequestOmitsVerificationFields(t *testing.T) {
	role := Role(RoleDo)
	if role == nil {
		t.Fatal("Role(RoleDo) returned nil")
	}

	reqJSON := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal"},"step":{"index":2},"paths":{"workspace_dir":"/tmp"},"task_state":{"plan":{"acceptance_criteria":[{"id":"AC-1","text":"ok","checks":[{"id":"CHK-1","command":"true","expected_exit_codes":[0]}]}],"do_steps":[{"id":"DO-1","text":"do it"}]}}}`)

	mapped, err := role.MapRequest(contracts.RawAgentRequest(reqJSON))
	if err != nil {
		t.Fatalf("role.MapRequest() error = %v", err)
	}

	data, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("json.Marshal(mapped) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}

	doInput, ok := payload["do_input"].(map[string]any)
	if !ok {
		t.Fatalf("payload[\"do_input\"] type = %T, want map[string]any", payload["do_input"])
	}
	criteriaAny, ok := doInput["acceptance_criteria"].([]any)
	if !ok {
		t.Fatalf("do_input[\"acceptance_criteria\"] type = %T, want []any", doInput["acceptance_criteria"])
	}
	if len(criteriaAny) != 1 {
		t.Fatalf("len(criteriaAny) = %d, want 1", len(criteriaAny))
	}

	ac, ok := criteriaAny[0].(map[string]any)
	if !ok {
		t.Fatalf("criteriaAny[0] type = %T, want map[string]any", criteriaAny[0])
	}

	if _, hasChecks := ac["checks"]; hasChecks {
		t.Fatalf("do_input.acceptance_criteria unexpectedly contains checks")
	}
}

func TestCheckRoleMapRequestReceivesVerificationChecks(t *testing.T) {
	role := Role(RoleCheck)
	if role == nil {
		t.Fatal("Role(RoleCheck) returned nil")
	}

	reqJSON := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal"},"step":{"index":3},"paths":{"workspace_dir":"/tmp"},"task_state":{"plan":{"acceptance_criteria":[{"id":"AC-1","text":"ok","checks":[{"id":"CHK-1","command":"true","expected_exit_codes":[0]}]}],"do_steps":[{"id":"DO-1","text":"do it"}]},"do":{"executed_step_ids":["DO-1"]}}}`)

	mapped, err := role.MapRequest(contracts.RawAgentRequest(reqJSON))
	if err != nil {
		t.Fatalf("role.MapRequest() error = %v", err)
	}

	data, err := json.Marshal(mapped)
	if err != nil {
		t.Fatalf("json.Marshal(mapped) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}
	checkInput, ok := payload["check_input"].(map[string]any)
	if !ok {
		t.Fatalf("payload[\"check_input\"] type = %T, want map[string]any", payload["check_input"])
	}
	criteriaAny, ok := checkInput["acceptance_criteria"].([]any)
	if !ok || len(criteriaAny) != 1 {
		t.Fatalf("check_input acceptance criteria = %#v, want one entry", checkInput["acceptance_criteria"])
	}
	ac, ok := criteriaAny[0].(map[string]any)
	if !ok {
		t.Fatalf("criteriaAny[0] type = %T, want map[string]any", criteriaAny[0])
	}
	checks, ok := ac["checks"].([]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("ac[\"checks\"] = %#v, want one check", ac["checks"])
	}
}

func TestAllRolesImplementRoleContract(t *testing.T) {
	t.Parallel()

	expectedRoles := []string{RolePlan, RoleDo, RoleCheck, RoleAct}

	for _, name := range expectedRoles {
		role := Role(name)
		if role == nil {
			t.Errorf("Role(%q) returned nil", name)
			continue
		}
		if role.Name() != name {
			t.Errorf("role.Name() = %q, want %q", role.Name(), name)
		}
	}
}

func TestAllRolesReturnValidSchemas(t *testing.T) {
	t.Parallel()

	expectedRoles := []string{RolePlan, RoleDo, RoleCheck, RoleAct}

	for _, name := range expectedRoles {
		role := Role(name)
		if role == nil {
			t.Errorf("Role(%q) returned nil", name)
			continue
		}

		schemas := role.Schemas()
		if schemas.InputSchema == "" {
			t.Errorf("role %q has empty InputSchema", name)
		}
		if schemas.OutputSchema == "" {
			t.Errorf("role %q has empty OutputSchema", name)
		}
		// Verify schemas are valid JSON
		if !json.Valid([]byte(schemas.InputSchema)) {
			t.Errorf("role %q InputSchema is not valid JSON", name)
		}
		if !json.Valid([]byte(schemas.OutputSchema)) {
			t.Errorf("role %q OutputSchema is not valid JSON", name)
		}
	}
}

func TestCheckAndActSchemasRejectUnsupportedVerdicts(t *testing.T) {
	t.Parallel()

	checkRole := Role(RoleCheck)
	if checkRole == nil {
		t.Fatal("Role(check) returned nil")
	}
	checkOutput := `{"status":"ok","summary":"done","check_output":{"acceptance_results":[],"verdict":"UNKNOWN"}}`
	assertSchemaInvalid(t, checkRole.Schemas().OutputSchema, checkOutput)

	actRole := Role(RoleAct)
	if actRole == nil {
		t.Fatal("Role(act) returned nil")
	}
	actInput := `{"run":{"id":"run-1","iteration":1},"task":{"id":"norma-1","goal":"goal"},"step":{"index":4},"paths":{"workspace_dir":"/tmp/work"},"act_input":{"verdict":"UNKNOWN","acceptance_results":[]}}`
	assertSchemaInvalid(t, actRole.Schemas().InputSchema, actInput)

	uppercaseCheckOutput := `{"status":"ok","summary":"done","check_output":{"acceptance_results":[],"verdict":"PASS"}}`
	assertSchemaInvalid(t, checkRole.Schemas().OutputSchema, uppercaseCheckOutput)

	uppercaseActInput := `{"run":{"id":"run-1","iteration":1},"task":{"id":"norma-1","goal":"goal"},"step":{"index":4},"paths":{"workspace_dir":"/tmp/work"},"act_input":{"verdict":"FAIL","acceptance_results":[]}}`
	assertSchemaInvalid(t, actRole.Schemas().InputSchema, uppercaseActInput)
}

func TestActSchemaRejectsRollbackDecision(t *testing.T) {
	t.Parallel()

	actRole := Role(RoleAct)
	if actRole == nil {
		t.Fatal("Role(act) returned nil")
	}

	actOutput := `{"status":"ok","summary":"done","act_output":{"decision":"rollback"}}`
	assertSchemaInvalid(t, actRole.Schemas().OutputSchema, actOutput)
}

func assertSchemaInvalid(t *testing.T, schema, payload string) {
	t.Helper()

	result, err := gojsonschema.Validate(
		gojsonschema.NewStringLoader(schema),
		gojsonschema.NewStringLoader(payload),
	)
	if err != nil {
		t.Fatalf("validate schema: %v", err)
	}
	if result.Valid() {
		t.Fatalf("schema unexpectedly accepted payload: %s", payload)
	}
}

func TestAllRolesMapResponseReturnsAgentResponse(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		response string
	}{
		{"plan", `{"status":"ok","summary":"done","plan_output":{"acceptance_criteria":[],"do_steps":[]}}`},
		{"do", `{"status":"ok","summary":"done","do_output":{"executed_step_ids":[]}}`},
		{"check", `{"status":"ok","summary":"done","check_output":{"acceptance_results":[],"verdict":"pass"}}`},
		{"act", `{"status":"ok","summary":"done","act_output":{"decision":"close"}}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			role := Role(tc.name)
			if role == nil {
				t.Fatalf("Role(%q) returned nil", tc.name)
			}

			resp, err := role.MapResponse([]byte(tc.response))
			if err != nil {
				t.Fatalf("MapResponse() error = %v", err)
			}

			if resp.Status != "ok" {
				t.Errorf("resp.Status = %q, want %q", resp.Status, "ok")
			}
			if resp.Summary != "done" {
				t.Errorf("resp.Summary = %q, want %q", resp.Summary, "done")
			}
		})
	}
}

func TestAllRolesMapRequestAcceptsValidJSON(t *testing.T) {
	t.Parallel()

	planReq := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal","acceptance_criteria":[{"id":"AC1","text":"test","verify_hints":[]}]},"step":{"index":1},"paths":{"workspace_dir":"/tmp"},"task_state":{}}`)

	// Do needs plan in task_state
	doReq := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal"},"step":{"index":2},"paths":{"workspace_dir":"/tmp"},"task_state":{"plan":{"acceptance_criteria":[{"id":"AC1","text":"test","checks":[]}],"do_steps":[]}}}`)

	// Check needs plan and do in task_state
	checkReq := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal"},"step":{"index":3},"paths":{"workspace_dir":"/tmp"},"task_state":{"plan":{"acceptance_criteria":[{"id":"AC1","text":"test","checks":[]}],"do_steps":[]},"do":{"executed_step_ids":[]}}}`)

	// Act needs check in task_state
	actReq := []byte(`{"run":{"id":"run-1","iteration":1},"task":{"id":"task-1","goal":"goal"},"step":{"index":4},"paths":{"workspace_dir":"/tmp"},"task_state":{"check":{"verdict":"pass","acceptance_results":[]}}}`)

	testCases := []struct {
		name    string
		request []byte
	}{
		{"plan", planReq},
		{"do", doReq},
		{"check", checkReq},
		{"act", actReq},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			role := Role(tc.name)
			if role == nil {
				t.Fatalf("Role(%q) returned nil", tc.name)
			}

			_, err := role.MapRequest(contracts.RawAgentRequest(tc.request))
			if err != nil {
				t.Errorf("MapRequest() error = %v", err)
			}
		})
	}
}
