package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func cashierPerms() ([]Permission, []FieldPolicy) {
	id := uuid.New()
	perms := []Permission{
		{RoleID: id, Resource: "check", Action: "read"},
		{RoleID: id, Resource: "check", Action: "create"},
	}
	fields := []FieldPolicy{
		{RoleID: id, Resource: "check", Field: "total"},
		{RoleID: id, Resource: "check", Field: "items"},
	}
	return perms, fields
}

func kitchenPerms() ([]Permission, []FieldPolicy) {
	id := uuid.New()
	perms := []Permission{
		{RoleID: id, Resource: "order", Action: "read"},
		{RoleID: id, Resource: "order", Action: "update"},
	}
	fields := []FieldPolicy{
		{RoleID: id, Resource: "order", Field: "items"},
		{RoleID: id, Resource: "order", Field: "notes"},
	}
	return perms, fields
}

func managerPerms() ([]Permission, []FieldPolicy) {
	id := uuid.New()
	perms := []Permission{{RoleID: id, Resource: "*", Action: "*"}}
	fields := []FieldPolicy{{RoleID: id, Resource: "*", Field: "*"}}
	return perms, fields
}

func TestPermSet_Can(t *testing.T) {
	perms, _ := cashierPerms()
	ps := NewPermSet(perms, nil)

	assert.True(t, ps.Can("check", "read"))
	assert.True(t, ps.Can("check", "create"))
	assert.False(t, ps.Can("check", "delete"))
	assert.False(t, ps.Can("order", "read"))
}

func TestPermSet_CanSeeField(t *testing.T) {
	_, fields := cashierPerms()
	ps := NewPermSet(nil, fields)

	assert.True(t, ps.CanSeeField("check", "total"))
	assert.True(t, ps.CanSeeField("check", "items"))
	assert.False(t, ps.CanSeeField("check", "discount"))
	assert.False(t, ps.CanSeeField("order", "items"))
}

func TestPermSet_WildcardManager(t *testing.T) {
	perms, fields := managerPerms()
	ps := NewPermSet(perms, fields)

	assert.True(t, ps.Can("check", "delete"))
	assert.True(t, ps.Can("order", "anything"))
	assert.True(t, ps.CanSeeField("check", "discount"))
	assert.True(t, ps.CanSeeField("payment", "card_number"))
}

func TestPermSet_Empty_DefaultDeny(t *testing.T) {
	ps := NewPermSet(nil, nil)

	assert.False(t, ps.Can("check", "read"))
	assert.False(t, ps.CanSeeField("check", "total"))
}

func TestPermSet_Merge_Union(t *testing.T) {
	cashPerms, cashFields := cashierPerms()
	kitPerms, kitFields := kitchenPerms()

	cashier := NewPermSet(cashPerms, cashFields)
	kitchen := NewPermSet(kitPerms, kitFields)
	merged := cashier.Merge(kitchen)

	// Cashier rights preserved.
	assert.True(t, merged.Can("check", "read"))
	assert.True(t, merged.CanSeeField("check", "total"))

	// Kitchen rights added.
	assert.True(t, merged.Can("order", "read"))
	assert.True(t, merged.CanSeeField("order", "items"))

	// No expansion beyond union.
	assert.False(t, merged.Can("check", "delete"))
	assert.False(t, merged.CanSeeField("order", "price"))
}

func TestPermSet_Merge_WithWildcard(t *testing.T) {
	cashPerms, cashFields := cashierPerms()
	mgrPerms, mgrFields := managerPerms()

	cashier := NewPermSet(cashPerms, cashFields)
	manager := NewPermSet(mgrPerms, mgrFields)

	merged := cashier.Merge(manager)
	assert.True(t, merged.Can("anything", "delete"))
	assert.True(t, merged.CanSeeField("anything", "secret_field"))
}

func TestPermSet_Merge_Idempotent(t *testing.T) {
	perms, fields := cashierPerms()
	ps := NewPermSet(perms, fields)
	merged := ps.Merge(ps)

	assert.True(t, merged.Can("check", "read"))
	assert.False(t, merged.Can("check", "delete"))
}
