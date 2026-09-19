package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	paymentpub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

type fixedSaleReader struct {
	paid, pending int64
}

func (f fixedSaleReader) TotalPaidForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return f.paid, nil
}

func (f fixedSaleReader) PendingTotalForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return f.pending, nil
}

var _ paymentpub.SaleReader = fixedSaleReader{}

func newCheckServiceWithSales(sales paymentpub.SaleReader) *service.CheckService {
	return service.NewCheckService(service.CheckParams{
		DB:         sharedPool,
		CheckRepo:  repo.NewCheckRepo(),
		TableRepo:  repo.NewTableRepo(),
		OrderRepo:  repo.NewOrderRepo(),
		SaleReader: sales,
		Logger:     zap.NewNop(),
	})
}

func placeTestOrder(t *testing.T, ctx context.Context, orders *service.OrderService, checkID *uuid.UUID) domain.Order {
	t.Helper()
	o, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      checkID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{
			{ProductID: uuid.New(), ProductName: "Lahmacun", ProductCurrency: "TRY", Quantity: 2, UnitPriceAmount: 15000},
		},
	})
	require.NoError(t, err)
	return o
}

func acceptTestOrder(t *testing.T, ctx context.Context, orders *service.OrderService, orderID uuid.UUID) domain.Order {
	t.Helper()
	o, err := orders.Accept(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), orderID, staffA)
	require.NoError(t, err)
	return o
}

func orderStatus(t *testing.T, ctx context.Context, orders *service.OrderService, orderID uuid.UUID) domain.OrderStatus {
	t.Helper()
	o, err := orders.GetByID(ctx, tenantA, orderID)
	require.NoError(t, err)
	return o.Status
}

func countOrderEvents(t *testing.T, ctx context.Context, orderID uuid.UUID, eventType, status string) int {
	t.Helper()
	var n int
	err := sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM pos_outbox
			WHERE aggregate_id = $1 AND event_type = $2 AND payload->>'status' = $3
		`, orderID.String(), eventType, status).Scan(&n)
	})
	require.NoError(t, err)
	return n
}

// TestIntegration_OrderAdvance_OnlyKitchenTargets is the regression for the
// /advance privilege escalation and the "bogus status = 500" finding.
func TestIntegration_OrderAdvance_OnlyKitchenTargets(t *testing.T) {
	ctx := context.Background()
	checks := newCheckServiceWithSales(zeroSaleReader{})
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	pending := placeTestOrder(t, ctx, orders, &c.ID)
	accepted := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &c.ID).ID)

	tests := []struct {
		name    string
		orderID uuid.UUID
		target  domain.OrderStatus
		wantErr error
	}{
		{"unknown status", accepted.ID, "bogus", service.ErrInvalidOrderStatus},
		{"empty status", accepted.ID, "", service.ErrInvalidOrderStatus},
		{"back to pending", accepted.ID, domain.OrderStatusPending, service.ErrInvalidOrderStatus},
		{"accept via advance", pending.ID, domain.OrderStatusAccepted, service.ErrUseDedicatedEndpoint},
		{"reject via advance", pending.ID, domain.OrderStatusRejected, service.ErrUseDedicatedEndpoint},
		{"cancel via advance", accepted.ID, domain.OrderStatusCancelled, service.ErrUseDedicatedEndpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := orders.AdvanceStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), tt.orderID, tt.target)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
	assert.Equal(t, domain.OrderStatusPending, orderStatus(t, ctx, orders, pending.ID))
	assert.Equal(t, domain.OrderStatusAccepted, orderStatus(t, ctx, orders, accepted.ID))

	advanced, err := orders.AdvanceStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), accepted.ID, domain.OrderStatusPreparing)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusPreparing, advanced.Status)
	assert.Len(t, advanced.Items, 1, "transition responses must carry the order's items")
}

func TestIntegration_OrderCancel(t *testing.T) {
	ctx := context.Background()
	checks := newCheckServiceWithSales(zeroSaleReader{})
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	o := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &c.ID).ID)
	assert.Len(t, o.Items, 1, "accept response must carry the order's items")

	cancelled, err := orders.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), o.ID, staffA)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusCancelled, cancelled.Status)
	assert.Len(t, cancelled.Items, 1)
	assert.Equal(t, 1, countOrderEvents(t, ctx, o.ID, "order.status_changed", "cancelled"))

	_, err = orders.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), o.ID, staffA)
	assert.ErrorIs(t, err, pub.ErrInvalidTransition)

	_, err = orders.Cancel(context.Background(), tenantA, branchPrincipal(branchB), placeTestOrder(t, ctx, orders, &c.ID).ID, staffA)
	assert.ErrorIs(t, err, pub.ErrBranchForbidden)
}

// TestIntegration_CheckCancel_CancelsLiveOrders is the "ghost ticket"
// regression: cancelling a check used to leave its pending/accepted/
// preparing/ready orders live on the kitchen display.
func TestIntegration_CheckCancel_CancelsLiveOrders(t *testing.T) {
	ctx := context.Background()
	checks := newCheckServiceWithSales(zeroSaleReader{})
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)

	pending := placeTestOrder(t, ctx, orders, &c.ID)
	accepted := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &c.ID).ID)
	ready := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &c.ID).ID)
	for _, s := range []domain.OrderStatus{domain.OrderStatusPreparing, domain.OrderStatusReady} {
		_, err := orders.AdvanceStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), ready.ID, s)
		require.NoError(t, err)
	}
	delivered := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &c.ID).ID)
	for _, s := range []domain.OrderStatus{domain.OrderStatusPreparing, domain.OrderStatusReady, domain.OrderStatusDelivered} {
		_, err := orders.AdvanceStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), delivered.ID, s)
		require.NoError(t, err)
	}
	rejected, err := orders.Reject(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), placeTestOrder(t, ctx, orders, &c.ID).ID, staffA, "stok yok")
	require.NoError(t, err)
	assert.Equal(t, "stok yok", rejected.RejectionReason)

	_, err = checks.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	for _, o := range []domain.Order{pending, accepted, ready} {
		assert.Equal(t, domain.OrderStatusCancelled, orderStatus(t, ctx, orders, o.ID))
		assert.Equal(t, 1, countOrderEvents(t, ctx, o.ID, "order.status_changed", "cancelled"),
			"each cascaded order must get its own cancellation event")
	}
	assert.Equal(t, domain.OrderStatusDelivered, orderStatus(t, ctx, orders, delivered.ID), "served food stays delivered")
	assert.Equal(t, domain.OrderStatusRejected, orderStatus(t, ctx, orders, rejected.ID))
	assert.Equal(t, 0, countOrderEvents(t, ctx, delivered.ID, "order.status_changed", "cancelled"))

	active, err := orders.ListActiveByBranch(ctx, tenantA, branchA)
	require.NoError(t, err)
	for _, o := range active {
		if o.CheckID != nil {
			assert.NotEqual(t, c.ID, *o.CheckID, "no order of a cancelled check may stay on the kitchen display")
		}
	}
}

// TestIntegration_OrderTransitions_RequireOpenCheck: a kitchen device must not
// be able to move an order that belongs to a closed or cancelled check.
func TestIntegration_OrderTransitions_RequireOpenCheck(t *testing.T) {
	ctx := context.Background()
	orders := newOrderService()

	paidChecks := newCheckServiceWithSales(fixedSaleReader{paid: 1_000_000})
	closed := openTestCheck(t, ctx, paidChecks)
	closedPending := placeTestOrder(t, ctx, orders, &closed.ID)
	closedAccepted := acceptTestOrder(t, ctx, orders, placeTestOrder(t, ctx, orders, &closed.ID).ID)
	_, err := paidChecks.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), closed.ID, staffA)
	require.NoError(t, err)

	checks := newCheckServiceWithSales(zeroSaleReader{})
	cancelled := openTestCheck(t, ctx, checks)
	cancelledPending := placeTestOrder(t, ctx, orders, &cancelled.ID)
	_, err = checks.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), cancelled.ID, staffA)
	require.NoError(t, err)

	type op func(uuid.UUID) error
	ops := map[string]op{
		"accept": func(id uuid.UUID) error {
			_, err := orders.Accept(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), id, staffA)
			return err
		},
		"reject": func(id uuid.UUID) error {
			_, err := orders.Reject(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), id, staffA, "x")
			return err
		},
		"advance": func(id uuid.UUID) error {
			_, err := orders.AdvanceStatus(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), id, domain.OrderStatusPreparing)
			return err
		},
		"cancel": func(id uuid.UUID) error {
			_, err := orders.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), id, staffA)
			return err
		},
	}
	for name, fn := range ops {
		for _, o := range []domain.Order{closedPending, closedAccepted, cancelledPending} {
			t.Run(name, func(t *testing.T) {
				assert.ErrorIs(t, fn(o.ID), pub.ErrCheckNotOpen)
			})
		}
	}
	assert.Equal(t, domain.OrderStatusPending, orderStatus(t, ctx, orders, closedPending.ID))
	assert.Equal(t, domain.OrderStatusAccepted, orderStatus(t, ctx, orders, closedAccepted.ID))

	takeaway := placeTestOrder(t, ctx, orders, nil)
	acceptTestOrder(t, ctx, orders, takeaway.ID)
}

func TestIntegration_CheckCancel_RefusedWhenPaymentsExist(t *testing.T) {
	ctx := context.Background()
	orders := newOrderService()

	tests := []struct {
		name  string
		sales fixedSaleReader
	}{
		{"completed payment", fixedSaleReader{paid: 5000}},
		{"payment awaiting fiscal result", fixedSaleReader{pending: 5000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checks := newCheckServiceWithSales(tt.sales)
			c := openTestCheck(t, ctx, checks)
			o := placeTestOrder(t, ctx, orders, &c.ID)

			_, err := checks.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
			assert.ErrorIs(t, err, service.ErrCheckHasPayments)

			got, err := checks.GetByID(ctx, tenantA, c.ID)
			require.NoError(t, err)
			assert.Equal(t, domain.CheckStatusOpen, got.Status)
			assert.Equal(t, domain.OrderStatusPending, orderStatus(t, ctx, orders, o.ID))
		})
	}
}
