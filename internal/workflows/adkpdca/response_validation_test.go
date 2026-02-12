package adkpdca

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
			name: "check role ignored",
			role: normaloop.RoleCheck,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: false,
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
