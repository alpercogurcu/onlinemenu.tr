package http_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
)

// managerRoleID / warehouseRoleID mirror the well-known system role ids in
// configs/opa/bundles/authz.rego. warehouseRoleID (…0007) is forward-declared
// in the rego (ADR-DATA-005 İlke 4) but not yet seeded in
// identity/000006_seed_system_roles.up.sql — see that file's comment for the
// follow-up. This test exercises the rego decision directly (bypassing the
// role_permissions table), which is exactly what OPA does at request time, so
// it is valid evidence of the policy today even though no real principal can
// hold the warehouse role until identity seeds it.
var (
	managerRoleID   = uuid.MustParse("00000001-0000-0000-0000-000000000006")
	warehouseRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000007")
	kitchenRoleID   = uuid.MustParse("00000001-0000-0000-0000-000000000004")
	cashierRoleID   = uuid.MustParse("00000001-0000-0000-0000-000000000001")
)

// inventoryManagementActions mirrors authz.rego's inventory_management_actions
// set (ADR-DATA-005 İlke 4 / ADR-DATA-006). Kept in sync manually since rego
// sets are not introspectable from Go without evaluating a query for them.
var inventoryManagementActions = []string{
	"inventory.level.read",
	"inventory.stock_item.read",
	"inventory.stock_item.create",
	"inventory.stock_item.update",
	"inventory.stock_item.delete",
	"inventory.warehouse.read",
	"inventory.warehouse.create",
	"inventory.warehouse.update",
	"inventory.warehouse.delete",
	"inventory.movement.read",
	"inventory.movement.create",
	"inventory.transfer_order.read",
	"inventory.transfer_order.create",
	"inventory.transfer_order.submit",
	"inventory.transfer_order.approve",
	"inventory.transfer_order.reject",
	"inventory.transfer_order.cancel",
	"inventory.transfer_order.fulfil",
	"inventory.shipment.read",
	"inventory.shipment.create",
	"inventory.shipment.advance",
	"inventory.shipment.receive",
	"inventory.shipment.cancel",
	"inventory.supply_policy.read",
	"inventory.purchase_receipt.create",
	"inventory.purchase_receipt.read",
}

func TestAuthz_InventoryManagement_ManagerAllowed(t *testing.T) {
	eng := newSmokeTestEngine(t)
	p := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{managerRoleID},
	}
	for _, action := range inventoryManagementActions {
		d, err := eng.Decide(context.Background(), action, p)
		require.NoError(t, err)
		assert.Truef(t, d.Allow, "manager should be allowed %q", action)
	}
}

func TestAuthz_InventoryManagement_WarehouseAllowed(t *testing.T) {
	eng := newSmokeTestEngine(t)
	p := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{warehouseRoleID},
	}
	for _, action := range inventoryManagementActions {
		d, err := eng.Decide(context.Background(), action, p)
		require.NoError(t, err)
		assert.Truef(t, d.Allow, "warehouse role should be allowed %q", action)
		assert.Equal(t, "branch", d.Scope)
	}
}

// TestAuthz_SupplyPolicyCreate_ManagerOnly is the ADR-DATA-007 regression
// test: "inventory.supply_policy.create" is deliberately absent from
// inventory_management_actions (authz.rego) so that manager is the ONLY
// role allowed to create/change a commercial procurement policy — warehouse
// gets read (covered by TestAuthz_InventoryManagement_WarehouseAllowed above)
// but never create. This guards against a future rego edit that folds create
// into the shared set and silently grants it to warehouse too.
func TestAuthz_SupplyPolicyCreate_ManagerOnly(t *testing.T) {
	eng := newSmokeTestEngine(t)

	manager := auth.Principal{
		PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		RoleIDs: []uuid.UUID{managerRoleID},
	}
	d, err := eng.Decide(context.Background(), "inventory.supply_policy.create", manager)
	require.NoError(t, err)
	assert.True(t, d.Allow, "manager should be allowed inventory.supply_policy.create")

	warehouse := auth.Principal{
		PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: uuid.New(), BranchID: uuid.New(),
		RoleIDs: []uuid.UUID{warehouseRoleID},
	}
	d, err = eng.Decide(context.Background(), "inventory.supply_policy.create", warehouse)
	require.NoError(t, err)
	assert.False(t, d.Allow, "warehouse must be denied inventory.supply_policy.create")
}

// TestAuthz_InventoryManagement_BranchRolesDenied is the ADR-DATA-005 İlke 4
// regression test: kitchen/cashier/waiter (and any branch-facing role other
// than manager/warehouse) must get NONE of the inventory management actions —
// visibility is route/permission absence, never a row-level opt-in flag.
func TestAuthz_InventoryManagement_BranchRolesDenied(t *testing.T) {
	eng := newSmokeTestEngine(t)
	cases := []struct {
		name string
		id   uuid.UUID
	}{
		{"kitchen", kitchenRoleID},
		{"cashier", cashierRoleID},
	}
	for _, tc := range cases {
		p := auth.Principal{
			PersonID: uuid.New(),
			Ctx:      auth.ContextStaff,
			TenantID: uuid.New(),
			BranchID: uuid.New(),
			RoleIDs:  []uuid.UUID{tc.id},
		}
		for _, action := range inventoryManagementActions {
			d, err := eng.Decide(context.Background(), action, p)
			require.NoError(t, err)
			assert.Falsef(t, d.Allow, "%s should be denied %q", tc.name, action)
		}
	}
}
