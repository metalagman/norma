package auth

// Decision represents an authorization decision.
type Decision int

const (
	Deny Decision = iota
	Allow
)

// Scope represents the required authorization scope for an operation.
type Scope int

const (
	ScopeOwnerOnly    Scope = iota // Only the owner can perform this operation
	ScopeCollaborator              // Owner or any collaborator
)

// Authorizer defines the interface for checking user permissions.
type Authorizer interface {
	// IsOwner returns true if the user is the bot owner.
	IsOwner(userID int64) bool
	// IsCollaborator returns true if user is a collaborator.
	IsCollaborator(userID int64) bool
}

// CanAccess checks if a user is authorized for a given scope.
// Returns Allow if authorized, Deny otherwise.
func CanAccess(a Authorizer, userID int64, required Scope) Decision {
	// Owner always has full access
	if a.IsOwner(userID) {
		return Allow
	}

	// Check collaborator - all collaborators have equal access
	switch required {
	case ScopeOwnerOnly:
		return Deny // Only owner can access
	case ScopeCollaborator:
		if a.IsCollaborator(userID) {
			return Allow
		}
		return Deny
	}

	return Deny
}
