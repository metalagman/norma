package pdca

import (
	"path/filepath"
	"testing"

	"github.com/normahq/norma/pkg/runtime/agentconfig"
)

func TestRoleMCPServersPlanRole(t *testing.T) {
	prev := resolveExecutablePath
	resolveExecutablePath = func() (string, error) { return "/tmp/norma", nil }
	t.Cleanup(func() { resolveExecutablePath = prev })

	servers, err := roleMCPServers(RolePlan, "./repo")
	if err != nil {
		t.Fatalf("roleMCPServers() error = %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}

	cfg, ok := servers[tasksMCPServerName]
	if !ok {
		t.Fatalf("servers missing %q key", tasksMCPServerName)
	}
	if cfg.Type != agentconfig.MCPServerTypeStdio {
		t.Fatalf("cfg.Type = %q, want %q", cfg.Type, agentconfig.MCPServerTypeStdio)
	}
	if len(cfg.Cmd) != 5 {
		t.Fatalf("len(cfg.Cmd) = %d, want 5", len(cfg.Cmd))
	}
	if cfg.Cmd[0] != "/tmp/norma" || cfg.Cmd[1] != "mcp" || cfg.Cmd[2] != "tasks" || cfg.Cmd[3] != "--working-dir" {
		t.Fatalf("cfg.Cmd = %v, want executable + mcp tasks --working-dir", cfg.Cmd)
	}
	if !filepath.IsAbs(cfg.Cmd[4]) {
		t.Fatalf("cfg.Cmd[4] = %q, want absolute repo path", cfg.Cmd[4])
	}
}

func TestRoleMCPServersNonPlanRoles(t *testing.T) {
	for _, roleName := range []string{RoleDo, RoleCheck, RoleAct} {
		t.Run(roleName, func(t *testing.T) {
			servers, err := roleMCPServers(roleName, "/repo")
			if err != nil {
				t.Fatalf("roleMCPServers() error = %v, want nil", err)
			}
			if servers != nil {
				t.Fatalf("servers = %v, want nil", servers)
			}
		})
	}
}

func TestRoleMCPServersPlanRoleRequiresWorkingDir(t *testing.T) {
	_, err := roleMCPServers(RolePlan, "   ")
	if err == nil {
		t.Fatal("roleMCPServers() error = nil, want non-nil")
	}
}
