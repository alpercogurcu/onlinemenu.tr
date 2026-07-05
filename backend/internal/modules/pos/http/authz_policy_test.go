package http_test

// Table plan (pos.table.read / pos.table.manage) OPA policy assertions
// (Sprint-5 Wave 1, docs/lessons-from-b2b.md item 1 — a grant is only
// verified when a positive case proves the role IS allowed, not just that
// unauthorized callers are rejected). TestRegisterRoutes_AllRoutesRequirePermission
// in authz_smoke_test.go only proves every route is *gated*; it would still
// pass if a rego action string were typo'd, since a typo makes every role
// 403. These tests exercise the rego decision directly against the well-known
// system role ids (mirrors inventory/http/authz_policy_test.go's pattern).

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
)

// Well-known system role ids, mirroring configs/opa/bundles/authz.rego's
// system_roles map. waiterRoleID (…0008) is forward-declared in the rego
// (mirrors warehouseRoleID's …0007 pattern) but not yet seeded in
// identity/000006_seed_system_roles.up.sql — seeding it is an identity-module
// follow-up outside this task's scope (see that map's comment). This test
// exercises the rego decision directly, which is valid evidence of the
// policy today even though no real principal can hold "waiter" until
// identity seeds it.
var (
	tablePolicyManagerID      = uuid.MustParse("00000001-0000-0000-0000-000000000006")
	tablePolicyShiftManagerID = uuid.MustParse("00000001-0000-0000-0000-000000000002")
	tablePolicyCashierID      = uuid.MustParse("00000001-0000-0000-0000-000000000001")
	tablePolicyKitchenID      = uuid.MustParse("00000001-0000-0000-0000-000000000004")
	tablePolicyBarID          = uuid.MustParse("00000001-0000-0000-0000-000000000005")
	tablePolicyWaiterID       = uuid.MustParse("00000001-0000-0000-0000-000000000008")
	tablePolicyDriverID       = uuid.MustParse("00000001-0000-0000-0000-000000000003")
)

func tablePolicyPrincipal(roleID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{roleID},
	}
}

func TestAuthz_PosTableRead_GrantedRoles(t *testing.T) {
	eng := newSmokeTestEngine(t)
	granted := []struct {
		name string
		id   uuid.UUID
	}{
		{"manager", tablePolicyManagerID},
		{"shift_manager", tablePolicyShiftManagerID},
		{"cashier", tablePolicyCashierID},
		{"waiter", tablePolicyWaiterID},
		{"kitchen", tablePolicyKitchenID},
		{"bar", tablePolicyBarID},
	}
	for _, tc := range granted {
		d, err := eng.Decide(context.Background(), "pos.table.read", tablePolicyPrincipal(tc.id))
		require.NoError(t, err)
		assert.Truef(t, d.Allow, "%s should be allowed pos.table.read", tc.name)
	}
}

func TestAuthz_PosTableRead_DeniedForDriver(t *testing.T) {
	eng := newSmokeTestEngine(t)
	d, err := eng.Decide(context.Background(), "pos.table.read", tablePolicyPrincipal(tablePolicyDriverID))
	require.NoError(t, err)
	assert.False(t, d.Allow, "driver should be denied pos.table.read (not a table-facing role)")
}

func TestAuthz_PosTableManage_ManagerAndShiftManagerAllowed(t *testing.T) {
	eng := newSmokeTestEngine(t)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{
		{"manager", tablePolicyManagerID},
		{"shift_manager", tablePolicyShiftManagerID},
	} {
		d, err := eng.Decide(context.Background(), "pos.table.manage", tablePolicyPrincipal(tc.id))
		require.NoError(t, err)
		assert.Truef(t, d.Allow, "%s should be allowed pos.table.manage", tc.name)
	}
}

// TestAuthz_PosTableManage_ReadOnlyRolesDenied is the regression test for the
// read/manage split: a role that can see the floor plan (cashier/waiter/
// kitchen/bar) must NOT be able to create/edit zones or tables or force a
// manual status change — that stays management-only.
func TestAuthz_PosTableManage_ReadOnlyRolesDenied(t *testing.T) {
	eng := newSmokeTestEngine(t)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{
		{"cashier", tablePolicyCashierID},
		{"waiter", tablePolicyWaiterID},
		{"kitchen", tablePolicyKitchenID},
		{"bar", tablePolicyBarID},
	} {
		d, err := eng.Decide(context.Background(), "pos.table.manage", tablePolicyPrincipal(tc.id))
		require.NoError(t, err)
		assert.Falsef(t, d.Allow, "%s should be denied pos.table.manage", tc.name)
	}
}
