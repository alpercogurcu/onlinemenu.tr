package service_test

// Branch-scope authorization tests (ADR-AUTH-001 layer 3,
// docs/lessons-from-b2b.md item 6 — "authz rules must be bound to a test or
// the work isn't done"). These run against the shared testcontainers pool
// from integration_test.go's TestMain.
//
// Pattern per rule: allowed branch -> success; foreign branch ->
// pub.ErrBranchForbidden; chain-wide principal (the realistic shape of a
// manager's membership, per identity module's "nil = chain-wide" contract)
// -> exempt.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

// branchB is a second branch in tenantA, distinct from branchA, used to
// assert that a principal belonging to ONE branch cannot act on another
// branch's resource.
var branchB = uuid.MustParse("cccccccc-0000-0000-0000-000000000002")

// branchPrincipal returns a staff principal scoped to a single branch.
func branchPrincipal(branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

// chainWidePrincipal returns a staff principal with no single-branch
// restriction (BranchID == uuid.Nil), the realistic shape of a manager's
// membership — exempt from branch-scope checks via
// auth.Principal.HasBranchAccess regardless of OPA scope.
func chainWidePrincipal() auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantA,
		BranchID: uuid.Nil,
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
}

func newOrderService() *service.OrderService {
	return service.NewOrderService(service.OrderParams{
		DB:        sharedPool,
		OrderRepo: repo.NewOrderRepo(),
		Logger:    zap.NewNop(),
	})
}

// ---------------------------------------------------------------------------
// Check authz
// ---------------------------------------------------------------------------

func TestCheckAuthz_Open(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	newReq := func() domain.Check {
		return domain.Check{BranchID: branchA, TableLabel: "Masa Authz", OpenedBy: uuid.New()}
	}

	t.Run("own branch may open", func(t *testing.T) {
		c, err := svc.Open(ctx, tenantA, branchPrincipal(branchA), newReq())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusOpen, c.Status)
	})

	t.Run("foreign branch is forbidden from opening", func(t *testing.T) {
		_, err := svc.Open(ctx, tenantA, branchPrincipal(branchB), newReq())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		c, err := svc.Open(ctx, tenantA, chainWidePrincipal(), newReq())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusOpen, c.Status)
	})
}

func TestCheckAuthz_Close(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	t.Run("owning branch may close", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		closed, err := svc.Close(ctx, tenantA, branchPrincipal(branchA), c.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusClosed, closed.Status)
	})

	t.Run("foreign branch is forbidden from closing", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		_, err := svc.Close(ctx, tenantA, branchPrincipal(branchB), c.ID, uuid.New())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		closed, err := svc.Close(ctx, tenantA, chainWidePrincipal(), c.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusClosed, closed.Status)
	})
}

func TestCheckAuthz_Cancel(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()

	t.Run("owning branch may cancel", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		cancelled, err := svc.Cancel(ctx, tenantA, branchPrincipal(branchA), c.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusCancelled, cancelled.Status)
	})

	t.Run("foreign branch is forbidden from cancelling", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		_, err := svc.Cancel(ctx, tenantA, branchPrincipal(branchB), c.ID, uuid.New())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		c := openTestCheck(t, ctx, svc)
		cancelled, err := svc.Cancel(ctx, tenantA, chainWidePrincipal(), c.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.CheckStatusCancelled, cancelled.Status)
	})
}

// ---------------------------------------------------------------------------
// Order authz
// ---------------------------------------------------------------------------

func newTestOrder(t *testing.T, ctx context.Context, svc *service.OrderService, branchID uuid.UUID) domain.Order {
	t.Helper()
	o, err := svc.Place(ctx, tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchID,
		OrderChannel: domain.OrderChannelTakeaway,
		Items: []domain.OrderItem{
			{ProductID: uuid.New(), ProductName: "Test Item", ProductCurrency: "TRY", Quantity: 1, UnitPriceAmount: 1000},
		},
	})
	require.NoError(t, err)
	return o
}

func TestOrderAuthz_Place(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()

	newReq := func(branchID uuid.UUID) domain.Order {
		return domain.Order{
			BranchID:     branchID,
			OrderChannel: domain.OrderChannelTakeaway,
			Items: []domain.OrderItem{
				{ProductID: uuid.New(), ProductName: "Test Item", ProductCurrency: "TRY", Quantity: 1, UnitPriceAmount: 1000},
			},
		}
	}

	t.Run("own branch may place", func(t *testing.T) {
		o, err := svc.Place(ctx, tenantA, branchPrincipal(branchA), newReq(branchA))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusPending, o.Status)
	})

	t.Run("foreign branch is forbidden from placing", func(t *testing.T) {
		_, err := svc.Place(ctx, tenantA, branchPrincipal(branchB), newReq(branchA))
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		o, err := svc.Place(ctx, tenantA, chainWidePrincipal(), newReq(branchA))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusPending, o.Status)
	})
}

func TestOrderAuthz_Accept(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()

	t.Run("owning branch may accept", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		updated, err := svc.Accept(ctx, tenantA, branchPrincipal(branchA), o.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusAccepted, updated.Status)
	})

	t.Run("foreign branch is forbidden from accepting", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		_, err := svc.Accept(ctx, tenantA, branchPrincipal(branchB), o.ID, uuid.New())
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		updated, err := svc.Accept(ctx, tenantA, chainWidePrincipal(), o.ID, uuid.New())
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusAccepted, updated.Status)
	})
}

func TestOrderAuthz_Reject(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()

	t.Run("owning branch may reject", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		updated, err := svc.Reject(ctx, tenantA, branchPrincipal(branchA), o.ID, uuid.New(), "stok yok")
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRejected, updated.Status)
	})

	t.Run("foreign branch is forbidden from rejecting", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		_, err := svc.Reject(ctx, tenantA, branchPrincipal(branchB), o.ID, uuid.New(), "stok yok")
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		o := newTestOrder(t, ctx, svc, branchA)
		updated, err := svc.Reject(ctx, tenantA, chainWidePrincipal(), o.ID, uuid.New(), "stok yok")
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRejected, updated.Status)
	})
}

func newAcceptedTestOrder(t *testing.T, ctx context.Context, svc *service.OrderService, branchID uuid.UUID) domain.Order {
	t.Helper()
	o := newTestOrder(t, ctx, svc, branchID)
	updated, err := svc.Accept(ctx, tenantA, chainWidePrincipal(), o.ID, uuid.New())
	require.NoError(t, err)
	return updated
}

func TestOrderAuthz_AdvanceStatus(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()

	t.Run("owning branch may advance", func(t *testing.T) {
		o := newAcceptedTestOrder(t, ctx, svc, branchA)
		updated, err := svc.AdvanceStatus(ctx, tenantA, branchPrincipal(branchA), o.ID, domain.OrderStatusPreparing)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusPreparing, updated.Status)
	})

	t.Run("foreign branch is forbidden from advancing", func(t *testing.T) {
		o := newAcceptedTestOrder(t, ctx, svc, branchA)
		_, err := svc.AdvanceStatus(ctx, tenantA, branchPrincipal(branchB), o.ID, domain.OrderStatusPreparing)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})

	t.Run("chain-wide principal is exempt", func(t *testing.T) {
		o := newAcceptedTestOrder(t, ctx, svc, branchA)
		updated, err := svc.AdvanceStatus(ctx, tenantA, chainWidePrincipal(), o.ID, domain.OrderStatusPreparing)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusPreparing, updated.Status)
	})
}
