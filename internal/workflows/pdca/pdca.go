package pdca

import (
	"sync"

	"github.com/metalagman/norma/internal/workflows/pdca/models"
	"github.com/metalagman/norma/internal/workflows/pdca/roles/registry"
)

const (
	RolePlan  = "plan"
	RoleDo    = "do"
	RoleCheck = "check"
	RoleAct   = "act"
)

var (
	roles    = make(map[string]models.Role)
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
func GetRole(name string) models.Role {
	initializeRoles()
	return roles[name]
}
