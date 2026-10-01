package service_test

// Branch POS preferences (pos_branch_settings) + simple order flow tests.
// These run against the shared testcontainers pool from integration_test.go's
// TestMain. Each test that flips a branch's order_flow uses a branch id of its
// own so branchA (the rest of the package's fixture branch) stays on the
// default 'full' flow throughout.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

func newBranchSettingsService() *service.BranchSettingsService {
	return service.NewBranchSettingsService(service.BranchSettingsParams{
		DB:       sharedPool,
		Settings: repo.NewBranchSettingsRepo(),
		Logger:   zap.NewNop(),
	})
}

// setOrderFlow flips a branch's order_flow as a chain manager would.
func setOrderFlow(t *testing.T, ctx context.Context, branchID uuid.UUID, flow domain.OrderFlow) {
	t.Helper()
	_, err := newBranchSettingsService().Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branchID, OrderFlow: &flow, UpdatedBy: staffA})
	require.NoError(t, err)
}

// openCheckAt opens a tableless check on the given branch.
func openCheckAt(t *testing.T, ctx context.Context, checks *service.CheckService, branchID uuid.UUID, label string) domain.Check {
	t.Helper()
	c, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchID,
		TableLabel: label,
		OpenedBy:   &staffA,
	})
	require.NoError(t, err)
	return c
}

// placeOrderAt places one staff order on the given branch/check.
func placeOrderAt(t *testing.T, ctx context.Context, orders *service.OrderService, branchID uuid.UUID, checkID *uuid.UUID) domain.Order {
	t.Helper()
	product := testProduct("Döner Dürüm", 20000)
	o, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchID,
		CheckID:      checkID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{
			{ProductID: product, ProductName: "Döner Dürüm", ProductCurrency: "TRY", Quantity: 1, UnitPriceAmount: 20000},
		},
	})
	require.NoError(t, err)
	return o
}

// countEvents counts outbox rows for one aggregate by event type, regardless
// of payload shape (countOrderEvents filters on payload status, which
// order.accepted does not carry).
func countEvents(t *testing.T, aggregateID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	require.NoError(t, sharedPool.WithTenantReadTx(context.Background(), tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM pos_outbox
			WHERE aggregate_id = $1 AND event_type = $2
		`, aggregateID.String(), eventType).Scan(&n)
	}))
	return n
}

func branchSettingsRowCount(t *testing.T, branchID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, sharedPool.WithTenantReadTx(context.Background(), tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM pos_branch_settings WHERE branch_id = $1`, branchID).Scan(&n)
	}))
	return n
}

// ---------------------------------------------------------------------------
// Settings endpoint semantics (GET defaults, PUT partial upsert, SEC-005)
// ---------------------------------------------------------------------------

// TestBranchSettings_Get_DefaultsWithoutRow: an untouched branch answers the
// defaults AND stays untouched — a GET must never create the row.
func TestBranchSettings_Get_DefaultsWithoutRow(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	svc := newBranchSettingsService()

	s, err := svc.Get(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), branch)
	require.NoError(t, err)
	assert.Equal(t, domain.WaiterCategoryLayoutTop, s.WaiterCategoryLayout)
	assert.Equal(t, domain.OrderFlowFull, s.OrderFlow)
	assert.Equal(t, branch, s.BranchID)

	assert.Equal(t, 0, branchSettingsRowCount(t, branch), "GET must not create a settings row")
}

// TestBranchSettings_Set_PartialUpsert: an omitted field keeps the current
// value (or the default on first write) — the frozen PUT contract.
func TestBranchSettings_Set_PartialUpsert(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	svc := newBranchSettingsService()

	side := domain.WaiterCategoryLayoutSide
	s, err := svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, WaiterCategoryLayout: &side, UpdatedBy: staffA})
	require.NoError(t, err)
	assert.Equal(t, domain.WaiterCategoryLayoutSide, s.WaiterCategoryLayout)
	assert.Equal(t, domain.OrderFlowFull, s.OrderFlow, "omitted order_flow must fall back to the default on first write")

	simple := domain.OrderFlowSimple
	updatedBy := uuid.New()
	s, err = svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, OrderFlow: &simple, UpdatedBy: updatedBy})
	require.NoError(t, err)
	assert.Equal(t, domain.WaiterCategoryLayoutSide, s.WaiterCategoryLayout, "omitted layout must keep the stored value")
	assert.Equal(t, domain.OrderFlowSimple, s.OrderFlow)
	assert.Equal(t, updatedBy, s.UpdatedBy)

	got, err := svc.Get(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), branch)
	require.NoError(t, err)
	assert.Equal(t, s.WaiterCategoryLayout, got.WaiterCategoryLayout, "values must round-trip through GET")
	assert.Equal(t, s.OrderFlow, got.OrderFlow)
	assert.Equal(t, 1, branchSettingsRowCount(t, branch), "two PUTs must upsert one row")
}

func TestBranchSettings_Set_InvalidValues(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	svc := newBranchSettingsService()

	badLayout := domain.WaiterCategoryLayout("bottom")
	_, err := svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, WaiterCategoryLayout: &badLayout, UpdatedBy: staffA})
	assert.ErrorIs(t, err, service.ErrInvalidWaiterCategoryLayout)

	badFlow := domain.OrderFlow("kds")
	_, err = svc.Set(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		service.SetBranchSettingsRequest{BranchID: branch, OrderFlow: &badFlow, UpdatedBy: staffA})
	assert.ErrorIs(t, err, service.ErrInvalidOrderFlow)

	assert.Equal(t, 0, branchSettingsRowCount(t, branch), "a rejected PUT must not write a row")
}

// TestBranchSettings_BranchScope is the SEC-005 matrix for both methods:
// own branch passes without any OPA scope, a foreign branch is forbidden,
// the chain manager (tenant scope) is exempt (exercised by every other test
// here via chainWideCtx).
func TestBranchSettings_BranchScope(t *testing.T) {
	ctx := context.Background()
	svc := newBranchSettingsService()
	branch := uuid.New()

	t.Run("own branch may read and write", func(t *testing.T) {
		p := branchPrincipal(branch)
		_, err := svc.Get(ctx, tenantA, p, branch)
		require.NoError(t, err)
		simple := domain.OrderFlowSimple
		_, err = svc.Set(ctx, tenantA, p, service.SetBranchSettingsRequest{BranchID: branch, OrderFlow: &simple, UpdatedBy: p.PersonID})
		require.NoError(t, err)
	})

	t.Run("foreign branch is forbidden", func(t *testing.T) {
		p := branchPrincipal(branchB)
		_, err := svc.Get(ctx, tenantA, p, branch)
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
		full := domain.OrderFlowFull
		_, err = svc.Set(ctx, tenantA, p, service.SetBranchSettingsRequest{BranchID: branch, OrderFlow: &full, UpdatedBy: p.PersonID})
		assert.ErrorIs(t, err, pub.ErrBranchForbidden)
	})
}

// ---------------------------------------------------------------------------
// Simple order flow: orders are born accepted
// ---------------------------------------------------------------------------

// TestSimpleFlow_StaffOrderBornAccepted: on a simple branch a POS order skips
// the counter-approval step — born accepted, with the acceptance bookkeeping
// filled, and BOTH the order.placed and order.accepted events recorded in the
// placement transaction (order.placed's payload carries no status, so the
// second event is what an event-tracking consumer sees the acceptance by).
func TestSimpleFlow_StaffOrderBornAccepted(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	setOrderFlow(t, ctx, branch, domain.OrderFlowSimple)
	checks, orders := newCheckService(), newOrderService()

	c := openCheckAt(t, ctx, checks, branch, "Masa Simple-1")
	o := placeOrderAt(t, ctx, orders, branch, &c.ID)

	assert.Equal(t, domain.OrderStatusAccepted, o.Status)
	require.NotNil(t, o.AcceptedAt, "a born-accepted order must carry accepted_at like an endpoint-accepted one")
	require.NotNil(t, o.AcceptedBy)

	fetched, err := orders.GetByID(context.Background(), tenantA, o.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusAccepted, fetched.Status, "the status must round-trip, not just decorate the response")

	assert.Equal(t, 1, countEvents(t, o.ID, "order.placed"))
	assert.Equal(t, 1, countEvents(t, o.ID, "order.accepted"))
}

// TestFullFlow_StaffOrderStaysPending pins the default: a branch without a
// settings row keeps producing pending orders with no accepted event.
func TestFullFlow_StaffOrderStaysPending(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	checks, orders := newCheckService(), newOrderService()

	c := openCheckAt(t, ctx, checks, branch, "Masa Full-1")
	o := placeOrderAt(t, ctx, orders, branch, &c.ID)

	assert.Equal(t, domain.OrderStatusPending, o.Status)
	assert.Nil(t, o.AcceptedAt)
	assert.Nil(t, o.AcceptedBy)
	assert.Equal(t, 1, countEvents(t, o.ID, "order.placed"))
	assert.Equal(t, 0, countEvents(t, o.ID, "order.accepted"))
}

// seedGuestTableAt is seedGuestTable for an arbitrary branch.
func seedGuestTableAt(t *testing.T, branchID uuid.UUID, name string) domain.Table {
	t.Helper()
	ctx := context.Background()
	tableRepo := repo.NewTableRepo()

	var table domain.Table
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantA, func(tx pgx.Tx) error {
		zone, err := tableRepo.CreateZone(ctx, tx, domain.TableZone{
			TenantID: tenantA, BranchID: branchID, Name: "QR " + name, IsActive: true,
		})
		if err != nil {
			return err
		}
		table, err = tableRepo.CreateTable(ctx, tx, domain.Table{
			TenantID: tenantA, BranchID: branchID, ZoneID: zone.ID,
			Name: name, Capacity: 4, IsActive: true,
		})
		return err
	}))
	return table
}

// TestSimpleFlow_GuestOrderBornAccepted: the guest QR path follows the same
// branch flow — the diner's screen polls the order status and must read
// "accepted" immediately, with accepted_by null (nobody accepted it; the
// branch's configuration did).
func TestSimpleFlow_GuestOrderBornAccepted(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	setOrderFlow(t, ctx, branch, domain.OrderFlowSimple)
	table := seedGuestTableAt(t, branch, "Masa Simple-QR")
	orders := newGuestOrderService()

	req := guestRequestFor(table)
	req.BranchID = branch
	result, err := orders.PlaceGuest(ctx, req, nil)
	require.NoError(t, err)
	assert.Equal(t, string(domain.OrderStatusAccepted), result.Status)

	o, err := orders.GetByID(ctx, tenantA, result.OrderID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusAccepted, o.Status)
	require.NotNil(t, o.AcceptedAt)
	assert.Nil(t, o.AcceptedBy, "an anonymous diner has no person row; auto-acceptance must not invent one")

	assert.Equal(t, 1, countEvents(t, result.OrderID, "order.placed"))
	assert.Equal(t, 1, countEvents(t, result.OrderID, "order.accepted"))
}

// ---------------------------------------------------------------------------
// Simple order flow: close delivers live orders
// ---------------------------------------------------------------------------

// coveringSaleReader reports every check as fully paid so Close never trips
// ErrInsufficientPayment in these flow tests.
type coveringSaleReader struct{}

func (coveringSaleReader) TotalPaidForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return 1 << 40, nil
}

func (coveringSaleReader) PendingTotalForCheck(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return 0, nil
}

// TestSimpleFlow_CloseDeliversLiveOrders is the Serdivan scenario: the branch
// never enters KDS statuses, the customer pays at the counter, and the close
// itself is the delivery signal — the check closes and its live orders land
// on 'delivered' in the same transaction, one order.status_changed event each.
func TestSimpleFlow_CloseDeliversLiveOrders(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	setOrderFlow(t, ctx, branch, domain.OrderFlowSimple)
	checks := newCheckServiceWithSales(coveringSaleReader{})
	orders := newOrderService()

	c := openCheckAt(t, ctx, checks, branch, "Masa Simple-Close")
	o1 := placeOrderAt(t, ctx, orders, branch, &c.ID) // born accepted (simple)
	o2 := placeOrderAt(t, ctx, orders, branch, &c.ID)

	closed, err := checks.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err, "a simple-flow check with live orders must close")
	assert.Equal(t, domain.CheckStatusClosed, closed.Status)

	for _, id := range []uuid.UUID{o1.ID, o2.ID} {
		assert.Equal(t, domain.OrderStatusDelivered, orderStatus(t, ctx, orders, id))
		assert.Equal(t, 1, countOrderEvents(t, ctx, id, "order.status_changed", string(domain.OrderStatusDelivered)),
			"each delivered order must record its own status_changed event")
	}
}

// TestFullFlow_CloseLeavesOrdersUntouched pins today's full-mode behaviour so
// the simple cascade provably changes nothing for a branch without a settings
// row: a fully paid check with live orders closes (the backend has never
// blocked that — the practical blocker is payment coverage, since live items
// count toward the total) and the orders keep their statuses.
func TestFullFlow_CloseLeavesOrdersUntouched(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	checks := newCheckServiceWithSales(coveringSaleReader{})
	orders := newOrderService()

	c := openCheckAt(t, ctx, checks, branch, "Masa Full-Close")
	o := placeOrderAt(t, ctx, orders, branch, &c.ID) // born pending (full)

	_, err := checks.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	assert.Equal(t, domain.OrderStatusPending, orderStatus(t, ctx, orders, o.ID),
		"full flow must not touch order statuses on close")
	assert.Equal(t, 0, countOrderEvents(t, ctx, o.ID, "order.status_changed", string(domain.OrderStatusDelivered)))
}

// TestFullFlow_Close_UnpaidLiveOrdersStillRejected pins the rule that locks a
// receipt-only branch today: live order items count toward the check total,
// so an unpaid close is refused — in BOTH flows (simple mode skips the status
// dance, never the money).
func TestFullFlow_Close_UnpaidLiveOrdersStillRejected(t *testing.T) {
	ctx := context.Background()
	simple := domain.OrderFlowSimple
	for _, tc := range []struct {
		name string
		flow *domain.OrderFlow
	}{
		{"full", nil},
		{"simple", &simple},
	} {
		t.Run(tc.name, func(t *testing.T) {
			branch := uuid.New()
			if tc.flow != nil {
				setOrderFlow(t, ctx, branch, *tc.flow)
			}
			checks := newCheckServiceWithSales(zeroSaleReader{})
			orders := newOrderService()

			c := openCheckAt(t, ctx, checks, branch, "Masa Unpaid-"+tc.name)
			o := placeOrderAt(t, ctx, orders, branch, &c.ID)

			_, err := checks.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
			assert.ErrorIs(t, err, service.ErrInsufficientPayment)

			status := orderStatus(t, ctx, orders, o.ID)
			assert.NotEqual(t, domain.OrderStatusDelivered, status,
				"a failed close must roll the deliver cascade back with it")
		})
	}
}

// TestSimpleFlow_CancelStillCancelsLiveOrders pins Cancel's unchanged
// semantics in simple mode: an adisyon cancelled by mistake must not book its
// food as served — the pre-existing cancel cascade (ghost-ticket fix) applies
// in both flows, and nothing is marked delivered.
func TestSimpleFlow_CancelStillCancelsLiveOrders(t *testing.T) {
	ctx := context.Background()
	branch := uuid.New()
	setOrderFlow(t, ctx, branch, domain.OrderFlowSimple)
	checks := newCheckServiceWithSales(zeroSaleReader{})
	orders := newOrderService()

	c := openCheckAt(t, ctx, checks, branch, "Masa Simple-Cancel")
	o := placeOrderAt(t, ctx, orders, branch, &c.ID)

	cancelled, err := checks.Cancel(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)
	assert.Equal(t, domain.CheckStatusCancelled, cancelled.Status)
	assert.Equal(t, domain.OrderStatusCancelled, orderStatus(t, ctx, orders, o.ID),
		"cancel must keep cancelling live orders, never deliver them")
}
