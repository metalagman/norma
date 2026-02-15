package pdca

import (
	"sync"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

var (
	roleMap  = make(map[string]contracts.Role)
	initOnce sync.Once
)

func initializeRoles() {
	initOnce.Do(func() {
		for name, role := range roles.DefaultRoles() {
			roleMap[name] = role
		}
	})
}

// GetRole returns the role implementation by name.
func GetRole(name string) contracts.Role {
	initializeRoles()
	return roleMap[name]
}
