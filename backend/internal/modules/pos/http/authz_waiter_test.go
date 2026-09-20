package http_test

// Garson (waiter) role matrix. The waiter takes orders at the table: it may
// read the catalog, open an adisyon and place/read orders, and nothing that
// moves money or undoes work. A positive case proves the grant is real (a
// typo'd rego action would 403 everyone); the negative cases pin the edge the
// product decision drew — payment, cash drawer, cancel/reject, reports.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthz_Waiter_MayTakeOrders(t *testing.T) {
	eng := newSmokeTestEngine(t)
	granted := []string{
		"catalog.category.read",
		"catalog.product.read",
		"catalog.modifier_group.read",
		"catalog.modifier.read",
		"catalog.menu.read",
		"catalog.menu_item.read",
		"pos.table.read",
		"pos.table.clean",
		"pos.check.open",
		"pos.check.read",
		"pos.order.place",
		"pos.order.read",
		"tenant.branch.read",
		"tenant.modules.read",
	}
	for _, action := range granted {
		d, err := eng.Decide(context.Background(), action, tablePolicyPrincipal(tablePolicyWaiterID))
		require.NoError(t, err)
		assert.Truef(t, d.Allow, "waiter should be allowed %s", action)
	}
}

func TestAuthz_Waiter_CannotTouchMoneyUndoWorkOrManage(t *testing.T) {
	eng := newSmokeTestEngine(t)
	denied := []string{
		// checks: closing is settlement, cancelling/moving rewrites the bill
		"pos.check.close",
		"pos.check.cancel",
		"pos.check.transfer",
		"pos.check.merge",
		// orders: the counter decides accept/reject/cancel; the kitchen advances
		"pos.order.accept",
		"pos.order.reject",
		"pos.order.advance",
		"pos.order.move_items",
		// money and the drawer
		"payment.sale.register",
		"payment.payment.read",
		"payment.cash_session.read",
		"payment.cash_session.open",
		"payment.cash_session.close",
		// reports, table plan management, QR
		"pos.report.read",
		"pos.table.manage",
		"storefront.qr.read",
		"storefront.qr.manage",
		// catalog is READ-ONLY for the waiter
		"catalog.product.create",
		"catalog.product.update",
		"catalog.product.delete",
		"catalog.category.create",
		"catalog.menu.create",
		"catalog.branch_override.manage",
	}
	for _, action := range denied {
		d, err := eng.Decide(context.Background(), action, tablePolicyPrincipal(tablePolicyWaiterID))
		require.NoError(t, err)
		assert.Falsef(t, d.Allow, "waiter should be denied %s", action)
	}
}

// Granting the waiter must not leak the same actions to the roles that share
// the surrounding rule sets: the driver holds neither catalog nor check access.
func TestAuthz_Waiter_GrantDoesNotWidenDriver(t *testing.T) {
	eng := newSmokeTestEngine(t)
	for _, action := range []string{"catalog.product.read", "pos.check.open", "pos.order.place"} {
		d, err := eng.Decide(context.Background(), action, tablePolicyPrincipal(tablePolicyDriverID))
		require.NoError(t, err)
		assert.Falsef(t, d.Allow, "driver should stay denied %s", action)
	}
}
