package pdca

import (
	"testing"

	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
)

func TestValidateStepResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		role    string
		resp    *models.AgentResponse
		wantErr bool
	}{
		{
			name: "plan ok with payload",
			role: normaloop.RolePlan,
			resp: &models.AgentResponse{
				Status: "ok",
				Plan:   &models.PlanOutput{},
			},
			wantErr: false,
		},
		{
			name: "plan ok missing payload",
			role: normaloop.RolePlan,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "plan stop without payload",
			role: normaloop.RolePlan,
			resp: &models.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "plan error status",
			role: normaloop.RolePlan,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: true,
		},
		{
			name: "do ok with payload",
			role: normaloop.RoleDo,
			resp: &models.AgentResponse{
				Status: "ok",
				Do:     &models.DoOutput{},
			},
			wantErr: false,
		},
		{
			name: "do ok missing payload",
			role: normaloop.RoleDo,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "do stop without payload",
			role: normaloop.RoleDo,
			resp: &models.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "do error status",
			role: normaloop.RoleDo,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: true,
		},
		{
			name: "check ok with payload",
			role: normaloop.RoleCheck,
			resp: &models.AgentResponse{
				Status: "ok",
				Check:  &models.CheckOutput{},
			},
			wantErr: false,
		},
		{
			name: "check ok missing payload",
			role: normaloop.RoleCheck,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "check error status",
			role: normaloop.RoleCheck,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: true,
		},
		{
			name: "act ok with payload",
			role: normaloop.RoleAct,
			resp: &models.AgentResponse{
				Status: "ok",
				Act:    &models.ActOutput{},
			},
			wantErr: false,
		},
		{
			name: "act ok missing payload",
			role: normaloop.RoleAct,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "unknown role",
			role: "unknown",
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name:    "nil response",
			role:    normaloop.RolePlan,
			resp:    nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateStepResponse(tc.role, tc.resp)
			if tc.wantErr && err == nil {
				t.Fatalf("validateStepResponse() expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateStepResponse() unexpected error: %v", err)
			}
		})
	}
}
