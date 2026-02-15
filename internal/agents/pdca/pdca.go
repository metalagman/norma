package pdca

import (
	"sync"

	"github.com/metalagman/norma/internal/agents/pdca/contracts"
	"github.com/metalagman/norma/internal/agents/pdca/roles/registry"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

var (
	roles    = make(map[string]contracts.Role)
	initOnce sync.Once
)

func initializeRoles() {
	initOnce.Do(func() {
		for name, role := range registry.DefaultRoles() {
			roles[name] = role
		}
	})
}

// GetRole returns the role implementation by name.
func GetRole(name string) contracts.Role {
	initializeRoles()
	return roles[name]
}
