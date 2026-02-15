package pdca

import (
	"testing"

	"github.com/metalagman/norma/internal/workflows/pdca/models"
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
			role: RolePlan,
			resp: &models.AgentResponse{
				Status: "ok",
				Plan:   &models.PlanOutput{},
			},
			wantErr: false,
		},
		{
			name: "plan ok missing payload",
			role: RolePlan,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "plan stop without payload",
			role: RolePlan,
			resp: &models.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "plan error status",
			role: RolePlan,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "do ok with payload",
			role: RoleDo,
			resp: &models.AgentResponse{
				Status: "ok",
				Do:     &models.DoOutput{},
			},
			wantErr: false,
		},
		{
			name: "do ok missing payload",
			role: RoleDo,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "do stop without payload",
			role: RoleDo,
			resp: &models.AgentResponse{
				Status: "stop",
			},
			wantErr: false,
		},
		{
			name: "do error status",
			role: RoleDo,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "check ok with payload",
			role: RoleCheck,
			resp: &models.AgentResponse{
				Status: "ok",
				Check:  &models.CheckOutput{},
			},
			wantErr: false,
		},
		{
			name: "check ok missing payload",
			role: RoleCheck,
			resp: &models.AgentResponse{
				Status: "ok",
			},
			wantErr: true,
		},
		{
			name: "check error status",
			role: RoleCheck,
			resp: &models.AgentResponse{
				Status: "error",
			},
			wantErr: false,
		},
		{
			name: "act ok with payload",
			role: RoleAct,
			resp: &models.AgentResponse{
				Status: "ok",
				Act:    &models.ActOutput{},
			},
			wantErr: false,
		},
		{
			name: "act ok missing payload",
			role: RoleAct,
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
			role:    RolePlan,
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
