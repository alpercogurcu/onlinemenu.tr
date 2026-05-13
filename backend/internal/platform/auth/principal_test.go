package auth

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func staffPrincipal(branchID uuid.UUID) Principal {
	return Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

func TestPrincipal_IsPreContext(t *testing.T) {
	pre := Principal{KeycloakSub: "sub123"}
	assert.True(t, pre.IsPreContext())
	assert.False(t, pre.IsStaff())
	assert.False(t, pre.IsCustomer())

	staff := staffPrincipal(uuid.New())
	assert.False(t, staff.IsPreContext())
}

func TestPrincipal_HasBranchAccess_BranchScoped(t *testing.T) {
	branchID := uuid.New()
	other := uuid.New()
	p := staffPrincipal(branchID)

	assert.True(t, p.HasBranchAccess(branchID))
	assert.False(t, p.HasBranchAccess(other))
}

func TestPrincipal_HasBranchAccess_ChainWide(t *testing.T) {
	// Chain-wide staff (uuid.Nil BranchID) can access any branch.
	p := staffPrincipal(uuid.Nil)

	assert.True(t, p.HasBranchAccess(uuid.New()))
	assert.True(t, p.HasBranchAccess(uuid.New()))
	assert.True(t, p.HasBranchAccess(uuid.Nil))
}

func TestPrincipal_HasBranchAccess_CustomerDenied(t *testing.T) {
	customer := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextCustomer,
	}
	assert.False(t, customer.HasBranchAccess(uuid.New()))
	assert.False(t, customer.HasBranchAccess(uuid.Nil))
}

func TestPrincipal_HasBranchAccess_PreContextDenied(t *testing.T) {
	pre := Principal{KeycloakSub: "sub"}
	assert.False(t, pre.HasBranchAccess(uuid.New()))
}
