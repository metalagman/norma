package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type testAuthorizer struct {
	ownerID       int64
	collaborators map[int64]bool
}

func newTestAuthorizer() *testAuthorizer {
	return &testAuthorizer{
		ownerID:       1,
		collaborators: make(map[int64]bool),
	}
}

func (a *testAuthorizer) IsOwner(userID int64) bool {
	return a.ownerID == userID
}

func (a *testAuthorizer) IsCollaborator(userID int64) bool {
	return a.collaborators[userID]
}

func TestCanAccess_Owner(t *testing.T) {
	t.Parallel()

	authz := newTestAuthorizer()
	authz.ownerID = 100

	require.Equal(t, Allow, CanAccess(authz, 100, ScopeOwnerOnly))
	require.Equal(t, Allow, CanAccess(authz, 100, ScopeCollaborator))
}

func TestCanAccess_Collaborator(t *testing.T) {
	t.Parallel()

	authz := newTestAuthorizer()
	authz.ownerID = 1
	authz.collaborators[200] = true

	require.Equal(t, Allow, CanAccess(authz, 200, ScopeCollaborator))
	require.Equal(t, Deny, CanAccess(authz, 200, ScopeOwnerOnly))
}

func TestCanAccess_UnknownUser(t *testing.T) {
	t.Parallel()

	authz := newTestAuthorizer()
	authz.ownerID = 1

	require.Equal(t, Deny, CanAccess(authz, 999, ScopeOwnerOnly))
	require.Equal(t, Deny, CanAccess(authz, 999, ScopeCollaborator))
}

func TestCanAccess_OwnerOnlyScope(t *testing.T) {
	t.Parallel()

	authz := newTestAuthorizer()
	authz.ownerID = 1
	authz.collaborators[200] = true

	require.Equal(t, Allow, CanAccess(authz, 1, ScopeOwnerOnly))
	require.Equal(t, Deny, CanAccess(authz, 200, ScopeOwnerOnly))
	require.Equal(t, Deny, CanAccess(authz, 999, ScopeOwnerOnly))
}
