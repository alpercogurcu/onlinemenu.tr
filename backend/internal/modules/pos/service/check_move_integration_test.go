package service_test

// Masa taşıma / adisyon birleştirme / kalem taşıma (docs/pos-ux-spec.md §3c).
//
// These run against a real Postgres because every guarantee under test is a
// database one: the table statuses must flip in the SAME transaction as the
// check row, the merged source must keep its money out of the day-end report,
// and an order_item must end up billed to exactly one check.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/repo"
	"onlinemenu.tr/internal/modules/pos/service"
)

// withCollected builds a CheckService that sees a fixed amount as already
// collected on every check, so the payment-carrying refusals can be exercised
// without wiring the payment module into this package (pos must not import
// it — module isolation). It reuses order_lifecycle_integration_test.go's
// fixedSaleReader rather than adding a second stand-in of the same shape.
func withCollected(paid, pending int64) *service.CheckService {
	return newCheckServiceWithSales(fixedSaleReader{paid: paid, pending: pending})
}

func openCheckOnTable(t *testing.T, ctx context.Context, svc *service.CheckService, table domain.Table) domain.Check {
	t.Helper()
	c, err := svc.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID: branchA,
		TableID:  &table.ID,
		OpenedBy: &staffA,
	})
	require.NoError(t, err)
	return c
}

func placeOn(t *testing.T, ctx context.Context, orders *service.OrderService, checkID uuid.UUID, name string, price int64, qty int) domain.Order {
	t.Helper()
	o, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &checkID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{
			{ProductID: testProduct(name, price), ProductName: name, ProductCurrency: "TRY", Quantity: qty, UnitPriceAmount: price},
		},
	})
	require.NoError(t, err)
	return o
}

func tableStatus(t *testing.T, tableID uuid.UUID) domain.TableStatus {
	t.Helper()
	ctx := context.Background()
	var status domain.TableStatus
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		tbl, err := repo.NewTableRepo().GetTableByID(ctx, tx, tableID)
		status = tbl.Status
		return err
	}))
	return status
}

func checkTotal(t *testing.T, svc *service.CheckService, checkID uuid.UUID) int64 {
	t.Helper()
	_, total, err := svc.GetByIDWithTotal(context.Background(), tenantA, checkID)
	require.NoError(t, err)
	return total
}

// ---------------------------------------------------------------------------
// transfer
// ---------------------------------------------------------------------------

// TestCheckService_Transfer_FlipsBothTableStatuses is the acceptance test for
// §3c's "masa durumu aynı transaction'da sunucuda çevrilir": the cashier holds
// no pos.table.manage, so if the server did not do this the floor plan would
// show two occupied tables for one adisyon.
func TestCheckService_Transfer_FlipsBothTableStatuses(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	from := seedGuestTable(t, "TR-FROM-"+uuid.NewString()[:8])
	to := seedGuestTable(t, "TR-TO-"+uuid.NewString()[:8])

	c := openCheckOnTable(t, ctx, svc, from)
	require.Equal(t, domain.TableStatusOccupied, tableStatus(t, from.ID))

	moved, err := svc.Transfer(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, to.ID)
	require.NoError(t, err)

	assert.Equal(t, to.ID, *moved.TableID)
	assert.Equal(t, to.Name, moved.TableLabel, "the label follows the table, so receipts and the KDS agree")
	assert.Equal(t, domain.TableStatusEmpty, tableStatus(t, from.ID))
	assert.Equal(t, domain.TableStatusOccupied, tableStatus(t, to.ID))
}

func TestCheckService_Transfer_OccupiedTargetIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	from := seedGuestTable(t, "TR-OCC-A-"+uuid.NewString()[:8])
	busy := seedGuestTable(t, "TR-OCC-B-"+uuid.NewString()[:8])

	c := openCheckOnTable(t, ctx, svc, from)
	openCheckOnTable(t, ctx, svc, busy)

	_, err := svc.Transfer(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, busy.ID)
	assert.ErrorIs(t, err, pub.ErrTableOccupied)
	assert.Equal(t, domain.TableStatusOccupied, tableStatus(t, from.ID), "a refused transfer must leave the source table alone")
}

func TestCheckService_Transfer_ClosedCheckIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	from := seedGuestTable(t, "TR-CLS-A-"+uuid.NewString()[:8])
	to := seedGuestTable(t, "TR-CLS-B-"+uuid.NewString()[:8])

	c := openCheckOnTable(t, ctx, svc, from)
	_, err := svc.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, staffA)
	require.NoError(t, err)

	_, err = svc.Transfer(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, to.ID)
	assert.ErrorIs(t, err, pub.ErrCheckNotOpen)
}

func TestCheckService_Transfer_UnknownTableIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	from := seedGuestTable(t, "TR-404-"+uuid.NewString()[:8])
	c := openCheckOnTable(t, ctx, svc, from)

	_, err := svc.Transfer(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, uuid.New())
	assert.ErrorIs(t, err, pub.ErrTableNotFound)
}

// ---------------------------------------------------------------------------
// merge
// ---------------------------------------------------------------------------

// TestCheckService_Merge_MovesOrdersAndMarksSourceMerged covers the money
// half of §3c: the target's bill must grow by exactly what the source was
// worth, and the source must land on 'merged' rather than 'cancelled' — the
// distinction the day-end report reads.
func TestCheckService_Merge_MovesOrdersAndMarksSourceMerged(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	orders := newOrderService()
	sourceTable := seedGuestTable(t, "MG-SRC-"+uuid.NewString()[:8])
	targetTable := seedGuestTable(t, "MG-TGT-"+uuid.NewString()[:8])

	source := openCheckOnTable(t, ctx, svc, sourceTable)
	target := openCheckOnTable(t, ctx, svc, targetTable)
	placeOn(t, ctx, orders, source.ID, "Lahmacun", 9000, 2)
	placeOn(t, ctx, orders, target.ID, "Ayran", 4000, 1)

	require.Equal(t, int64(18000), checkTotal(t, svc, source.ID))
	require.Equal(t, int64(4000), checkTotal(t, svc, target.ID))

	merged, err := svc.Merge(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), target.ID, source.ID)
	require.NoError(t, err)
	assert.Equal(t, target.ID, merged.ID)

	assert.Equal(t, int64(22000), checkTotal(t, svc, target.ID), "the target now carries both adisyons")
	assert.Equal(t, int64(0), checkTotal(t, svc, source.ID))

	after, err := svc.GetByID(ctx, tenantA, source.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CheckStatusMerged, after.Status)
	require.NotNil(t, after.MergedIntoCheckID)
	assert.Equal(t, target.ID, *after.MergedIntoCheckID)
	assert.Nil(t, after.ClosedAt, "a merged check carries no closed_at, so it never enters a report window")
	assert.Equal(t, domain.TableStatusEmpty, tableStatus(t, sourceTable.ID))
	assert.Equal(t, domain.TableStatusOccupied, tableStatus(t, targetTable.ID))
}

// TestCheckService_Merge_PaidSourceIsRefused is §3c's `409 payments_present`:
// moving payment rows to another check would break the ÖKC/settlement trail.
func TestCheckService_Merge_PaidSourceIsRefused(t *testing.T) {
	ctx := context.Background()
	plain := newCheckService()
	sourceTable := seedGuestTable(t, "MG-PAY-A-"+uuid.NewString()[:8])
	targetTable := seedGuestTable(t, "MG-PAY-B-"+uuid.NewString()[:8])
	source := openCheckOnTable(t, ctx, plain, sourceTable)
	target := openCheckOnTable(t, ctx, plain, targetTable)

	withMoney := withCollected(5000, 0)
	_, err := withMoney.Merge(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), target.ID, source.ID)
	assert.ErrorIs(t, err, service.ErrCheckPaymentsPresent)

	// A fiscal-pending payment counts too: the receipt may still print.
	withPending := withCollected(0, 5000)
	_, err = withPending.Merge(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), target.ID, source.ID)
	assert.ErrorIs(t, err, service.ErrCheckPaymentsPresent)

	still, err := plain.GetByID(ctx, tenantA, source.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.CheckStatusOpen, still.Status, "a refused merge leaves the source untouched")
}

func TestCheckService_Merge_ClosedSideIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	sourceTable := seedGuestTable(t, "MG-CLS-A-"+uuid.NewString()[:8])
	targetTable := seedGuestTable(t, "MG-CLS-B-"+uuid.NewString()[:8])
	source := openCheckOnTable(t, ctx, svc, sourceTable)
	target := openCheckOnTable(t, ctx, svc, targetTable)

	_, err := svc.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), source.ID, staffA)
	require.NoError(t, err)

	_, err = svc.Merge(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), target.ID, source.ID)
	assert.ErrorIs(t, err, pub.ErrCheckNotOpen)
}

func TestCheckService_Merge_SameCheckIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	table := seedGuestTable(t, "MG-SAME-"+uuid.NewString()[:8])
	c := openCheckOnTable(t, ctx, svc, table)

	_, err := svc.Merge(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), c.ID, c.ID)
	assert.ErrorIs(t, err, service.ErrSameCheck)
}

// ---------------------------------------------------------------------------
// move-items
// ---------------------------------------------------------------------------

func TestCheckService_MoveItems_MovesTheBillWithTheLine(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	orders := newOrderService()
	sourceTable := seedGuestTable(t, "MV-SRC-"+uuid.NewString()[:8])
	targetTable := seedGuestTable(t, "MV-TGT-"+uuid.NewString()[:8])
	source := openCheckOnTable(t, ctx, svc, sourceTable)
	target := openCheckOnTable(t, ctx, svc, targetTable)

	stays := placeOn(t, ctx, orders, source.ID, "Çorba", 7500, 1)
	moves := placeOn(t, ctx, orders, source.ID, "Künefe", 12000, 2)
	require.Equal(t, int64(31500), checkTotal(t, svc, source.ID))

	returned, err := svc.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		source.ID, target.ID, []uuid.UUID{moves.Items[0].ID})
	require.NoError(t, err)
	assert.Equal(t, target.ID, returned.ID, "the response is the target check")

	assert.Equal(t, int64(7500), checkTotal(t, svc, source.ID))
	assert.Equal(t, int64(24000), checkTotal(t, svc, target.ID))

	// The emptied source order is cancelled; the untouched one is not.
	emptied, err := orders.GetByID(ctx, tenantA, moves.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusCancelled, emptied.Status)
	assert.Empty(t, emptied.Items)

	kept, err := orders.GetByID(ctx, tenantA, stays.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderStatusPending, kept.Status)
	assert.Len(t, kept.Items, 1)
}

// TestCheckService_MoveItems_ForeignItemIsRefused proves the whole request is
// rejected rather than partially applied — a cashier who selected two lines
// and got one moved has no way to tell which.
func TestCheckService_MoveItems_ForeignItemIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	orders := newOrderService()
	a := openCheckOnTable(t, ctx, svc, seedGuestTable(t, "MV-FGN-A-"+uuid.NewString()[:8]))
	b := openCheckOnTable(t, ctx, svc, seedGuestTable(t, "MV-FGN-B-"+uuid.NewString()[:8]))
	c := openCheckOnTable(t, ctx, svc, seedGuestTable(t, "MV-FGN-C-"+uuid.NewString()[:8]))

	mine := placeOn(t, ctx, orders, a.ID, "Pide", 9000, 1)
	theirs := placeOn(t, ctx, orders, c.ID, "Şalgam", 4500, 1)

	_, err := svc.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		a.ID, b.ID, []uuid.UUID{mine.Items[0].ID, theirs.Items[0].ID})
	assert.ErrorIs(t, err, service.ErrOrderItemNotFound)

	assert.Equal(t, int64(9000), checkTotal(t, svc, a.ID), "nothing moved")
	assert.Equal(t, int64(0), checkTotal(t, svc, b.ID))

	_, err = svc.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		a.ID, b.ID, []uuid.UUID{uuid.New()})
	assert.ErrorIs(t, err, service.ErrOrderItemNotFound, "an unknown id reads the same as someone else's")
}

// TestCheckService_MoveItems_PaidSourceIsRefusedWhenUncovered is §3c's
// `409 item_already_paid`: what stays behind must still cover what was paid.
func TestCheckService_MoveItems_PaidSourceIsRefusedWhenUncovered(t *testing.T) {
	ctx := context.Background()
	plain := newCheckService()
	orders := newOrderService()
	source := openCheckOnTable(t, ctx, plain, seedGuestTable(t, "MV-PAY-A-"+uuid.NewString()[:8]))
	target := openCheckOnTable(t, ctx, plain, seedGuestTable(t, "MV-PAY-B-"+uuid.NewString()[:8]))

	// Two separate lines of 10000 each: moving one must leave the other
	// behind, which is what the guard weighs against the collected amount.
	movable := placeOn(t, ctx, orders, source.ID, "İskender", 10000, 1)
	placeOn(t, ctx, orders, source.ID, "Pide", 10000, 1)
	require.Equal(t, int64(20000), checkTotal(t, plain, source.ID))

	// 15000 already collected: moving a 10000 line leaves 10000 behind, less
	// than what was paid.
	uncovered := withCollected(15000, 0)
	_, err := uncovered.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		source.ID, target.ID, []uuid.UUID{movable.Items[0].ID})
	assert.ErrorIs(t, err, service.ErrItemAlreadyPaid)

	// 5000 collected: 10000 stays behind, which still covers it.
	covered := withCollected(5000, 0)
	_, err = covered.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		source.ID, target.ID, []uuid.UUID{movable.Items[0].ID})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), checkTotal(t, plain, target.ID))
	assert.Equal(t, int64(10000), checkTotal(t, plain, source.ID), "what stays behind still covers what was paid")
}

func TestCheckService_MoveItems_ClosedTargetIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newCheckService()
	orders := newOrderService()
	source := openCheckOnTable(t, ctx, svc, seedGuestTable(t, "MV-CLS-A-"+uuid.NewString()[:8]))
	target := openCheckOnTable(t, ctx, svc, seedGuestTable(t, "MV-CLS-B-"+uuid.NewString()[:8]))
	order := placeOn(t, ctx, orders, source.ID, "Ayran", 4000, 1)

	_, err := svc.Close(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), target.ID, staffA)
	require.NoError(t, err)

	_, err = svc.MoveItems(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(),
		source.ID, target.ID, []uuid.UUID{order.Items[0].ID})
	assert.ErrorIs(t, err, pub.ErrCheckNotOpen)
}
