package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// systemRoleUUID mirrors the well-known system role IDs seeded in
// identity/000006_seed_system_roles.up.sql, matched by configs/opa/bundles/authz.rego.
func systemRoleUUID(t *testing.T, key string) uuid.UUID {
	t.Helper()
	ids := map[string]string{
		"cashier":       "00000001-0000-0000-0000-000000000001",
		"shift_manager": "00000001-0000-0000-0000-000000000002",
		"driver":        "00000001-0000-0000-0000-000000000003",
		"kitchen":       "00000001-0000-0000-0000-000000000004",
		"bar":           "00000001-0000-0000-0000-000000000005",
		"manager":       "00000001-0000-0000-0000-000000000006",
	}
	id, err := uuid.Parse(ids[key])
	require.NoError(t, err)
	return id
}

// newTestEngine loads the real policy bundle shipped with the platform and points
// the decision cache at an unreachable Redis address. Engine.Decide's getCached
// path swallows the resulting error and falls through to a live rego evaluation,
// so this exercises the actual policy without requiring a running Redis instance.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := NewEngine(
		EngineConfig{BundlePath: "../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)
	return eng
}

func TestEngine_Decide_Manager_AllowsEverything(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "manager")},
	}

	d, err := eng.Decide(context.Background(), "tenant.tenant.update", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "tenant", d.Scope)
}

func TestEngine_Decide_NoRoles_DeniesByDefault(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
	}

	for _, action := range []string{
		"catalog.product.read",
		"catalog.product.create",
		"tenant.tenant.read",
		"identity.role.read",
		"party.party.read",
		"hrcore.employee.read",
		"billing.invoice.read",
		"inventory.level.read",
		"pos.check.open",
		"payment.sale.register",
	} {
		d, err := eng.Decide(context.Background(), action, p)
		require.NoError(t, err)
		require.Falsef(t, d.Allow, "action %q must be denied for a roleless principal", action)
	}
}

func TestEngine_Decide_Cashier_CatalogReadOnly(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "cashier")},
	}

	d, err := eng.Decide(context.Background(), "catalog.product.read", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "branch", d.Scope)

	d, err = eng.Decide(context.Background(), "catalog.product.create", p)
	require.NoError(t, err)
	require.False(t, d.Allow)

	// Cashier has no seeded back-office access.
	d, err = eng.Decide(context.Background(), "tenant.tenant.read", p)
	require.NoError(t, err)
	require.False(t, d.Allow)
}

func TestEngine_Decide_ShiftManager_InventoryWrite(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "shift_manager")},
	}

	d, err := eng.Decide(context.Background(), "inventory.transaction.create", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "branch", d.Scope)

	d, err = eng.Decide(context.Background(), "inventory.transaction.create", Principal{
		RoleIDs: []uuid.UUID{systemRoleUUID(t, "cashier")},
	})
	require.NoError(t, err)
	require.False(t, d.Allow)
}

func TestEngine_Decide_Cashier_PosAndPaymentAccess(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "cashier")},
	}

	d, err := eng.Decide(context.Background(), "pos.check.open", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "branch", d.Scope)

	d, err = eng.Decide(context.Background(), "payment.sale.register", p)
	require.NoError(t, err)
	require.True(t, d.Allow)

	// Cashier registers sales but does not browse payment history — that is
	// reserved for shift_manager/manager reconciliation.
	d, err = eng.Decide(context.Background(), "payment.payment.read", p)
	require.NoError(t, err)
	require.False(t, d.Allow)
}

func TestEngine_Decide_Kitchen_OrderAdvanceOnly(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "kitchen")},
	}

	d, err := eng.Decide(context.Background(), "pos.order.advance", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "branch", d.Scope)

	// Kitchen reads/advances tickets but never opens/closes checks — that
	// stays with the counter roles (cashier/shift_manager).
	d, err = eng.Decide(context.Background(), "pos.check.open", p)
	require.NoError(t, err)
	require.False(t, d.Allow)
}

func TestEngine_Decide_ShiftManager_PaymentRead(t *testing.T) {
	eng := newTestEngine(t)
	p := Principal{
		PersonID: uuid.New(),
		Ctx:      ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{systemRoleUUID(t, "shift_manager")},
	}

	d, err := eng.Decide(context.Background(), "payment.payment.read", p)
	require.NoError(t, err)
	require.True(t, d.Allow)
	require.Equal(t, "branch", d.Scope)
}
