package service_test

// Server-side price validation on the staff order path (docs/pos-ux-spec.md
// bulgu #14 / P0). Until this existed, POST /pos/orders copied the client's
// unit_price_amount straight into the order, so any POS terminal could write
// its own prices.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/service"
)

func placeWithPrice(t *testing.T, ctx context.Context, orders *service.OrderService, checkID uuid.UUID, productID uuid.UUID, sent int64) (domain.Order, error) {
	t.Helper()
	return orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &checkID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{{
			ProductID:       productID,
			ProductName:     "İstemcinin verdiği ad",
			Quantity:        1,
			UnitPriceAmount: sent,
		}},
	})
}

func TestOrderService_Place_RejectsManipulatedUnitPrice(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	product := testProduct("Adana Kebap", 32000)

	for _, sent := range []int64{1, 31999, 32001} {
		_, err := placeWithPrice(t, ctx, orders, c.ID, product, sent)
		assert.ErrorIs(t, err, service.ErrPriceMismatch, "sent %d against a catalog price of 32000", sent)
	}

	assert.Equal(t, int64(0), checkTotal(t, checks, c.ID), "a rejected order must never reach the check")
}

// TestOrderService_Place_OverwritesClientSnapshotFields: rejecting on the
// price while trusting the tax rate next to it would leave the VAT
// breakdown — day-end report and fiscal receipt alike — client-controlled.
func TestOrderService_Place_OverwritesClientSnapshotFields(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	product := testProduct("Katalogdaki ad", 4500)

	placed, err := placeWithPrice(t, ctx, orders, c.ID, product, 4500)
	require.NoError(t, err)
	require.Len(t, placed.Items, 1)

	item := placed.Items[0]
	assert.Equal(t, "Katalogdaki ad", item.ProductName, "the name is the catalog's, not the client's")
	assert.Equal(t, int64(4500), item.ProductPriceAmount)
	assert.Equal(t, "TRY", item.ProductCurrency)
	assert.Equal(t, 1000, item.TaxRateBPS, "the tax rate comes from the catalog, never from the request")
}

func TestOrderService_Place_RejectsUnknownProduct(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)

	_, err := placeWithPrice(t, ctx, orders, c.ID, uuid.New(), 1000)
	assert.ErrorIs(t, err, service.ErrInvalidOrderLine)
}

// TestOrderService_Place_RejectsBeforeTouchingTheDatabase pins the ordering:
// the price check runs before the write transaction opens, so a manipulated
// order never takes a check row lock and never writes an outbox event the
// kitchen would act on.
func TestOrderService_Place_RejectsBeforeTouchingTheDatabase(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	product := testProduct("Künefe", 12000)

	_, err := placeWithPrice(t, ctx, orders, c.ID, product, 1)
	require.ErrorIs(t, err, service.ErrPriceMismatch)

	var orderCount, eventCount int
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, tenantA, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE check_id = $1`, c.ID).Scan(&orderCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM pos_outbox
			WHERE event_type = 'order.placed' AND payload->>'check_id' = $1
		`, c.ID.String()).Scan(&eventCount)
	}))
	assert.Equal(t, 0, orderCount)
	assert.Equal(t, 0, eventCount)
}

// TestOrderService_Place_PersistsModifierIDs is the POS↔backend contract for
// §3a: the options a line was ordered with must survive as ids, not only as
// the readable text in note. A note cannot be grouped by, reprinted at the
// right price, or told apart from a renamed option.
func TestOrderService_Place_PersistsModifierIDs(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)

	product := testProduct("Lahmacun", 9000)
	spicy := testModifier(product, 0)
	lavas := testModifier(product, 500)

	placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &c.ID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{{
			ProductID:       product,
			Quantity:        2,
			UnitPriceAmount: 9500, // 9000 + 0 + 500
			Note:            "Acılı | Lavaş(+5)",
			ModifierIDs:     []uuid.UUID{spicy, lavas},
		}},
	})
	require.NoError(t, err)
	require.Len(t, placed.Items, 1)
	assert.Equal(t, []uuid.UUID{spicy, lavas}, placed.Items[0].ModifierIDs)
	assert.Equal(t, "Acılı | Lavaş(+5)", placed.Items[0].Note, "note is stored verbatim, never parsed")

	// Read back through a fresh transaction: the ids are a column, not a
	// field the insert happened to echo.
	reread, err := orders.GetByID(ctx, tenantA, placed.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{spicy, lavas}, reread.Items[0].ModifierIDs)

	// A line with no options round-trips as empty, not as a phantom entry.
	plain, err := placeWithPrice(t, ctx, orders, c.ID, testProduct("Ayran", 4000), 4000)
	require.NoError(t, err)
	assert.Empty(t, plain.Items[0].ModifierIDs)

	// The base price alone is wrong once an option with a delta is selected.
	_, err = orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &c.ID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{{
			ProductID:       product,
			Quantity:        1,
			UnitPriceAmount: 9000,
			ModifierIDs:     []uuid.UUID{lavas},
		}},
	})
	assert.ErrorIs(t, err, service.ErrPriceMismatch)

	// An option belonging to another product cannot price this line.
	_, err = orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &c.ID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{{
			ProductID:       product,
			Quantity:        1,
			UnitPriceAmount: 9000,
			ModifierIDs:     []uuid.UUID{testModifier(testProduct("Başka Ürün", 1000), 0)},
		}},
	})
	assert.ErrorIs(t, err, service.ErrInvalidOrderLine)
}
