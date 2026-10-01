package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/domain"
	"onlinemenu.tr/internal/modules/pos/service"
)

// Seat (kuver) placement writes a brand-new column (pos/000011) and derives
// checks.pax from it inside the placement transaction; only a real database
// can tell whether the projection, the CHECK constraint and the GREATEST
// update agree.

func seatItem(t *testing.T, name string, price int64, seat int) domain.OrderItem {
	t.Helper()
	return domain.OrderItem{
		ProductID:       testProduct(name, price),
		ProductName:     name,
		ProductCurrency: "TRY",
		Quantity:        1,
		UnitPriceAmount: price,
		SeatNo:          seat,
	}
}

// TestOrderService_Place_SeatRoundTrip_RaisesPax is the happy path of the
// whole feature: seats persist and round-trip through GetByID, and the
// check's pax is raised to the highest seat named — with no separate "how
// many guests?" question ever asked.
func TestOrderService_Place_SeatRoundTrip_RaisesPax(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()

	c := openTestCheck(t, ctx, checks)
	require.Equal(t, 1, c.Pax, "fixture: an Open with no pax starts at the default of 1")

	placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &c.ID,
		OrderChannel: domain.OrderChannelDineIn,
		Items: []domain.OrderItem{
			seatItem(t, "Çay", 1000, 2),
			seatItem(t, "Kahve", 3000, 4),
			seatItem(t, "Su", 500, 0),
		},
	})
	require.NoError(t, err)
	require.Len(t, placed.Items, 3)
	assert.Equal(t, 2, placed.Items[0].SeatNo)
	assert.Equal(t, 4, placed.Items[1].SeatNo)
	assert.Equal(t, 0, placed.Items[2].SeatNo)

	// Items of one order share a created_at (transaction time), so the
	// re-read's ordering is not guaranteed — seats are compared by product
	// name, not by index.
	fetched, err := orders.GetByID(ctx, tenantA, placed.ID)
	require.NoError(t, err)
	require.Len(t, fetched.Items, 3)
	seats := make(map[string]int, len(fetched.Items))
	for _, it := range fetched.Items {
		seats[it.ProductName] = it.SeatNo
	}
	assert.Equal(t, map[string]int{"Çay": 2, "Kahve": 4, "Su": 0}, seats,
		"seat_no must round-trip through the projection")

	after, err := checks.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 4, after.Pax, "pax must be raised to the highest seat on the order")
}

// TestOrderService_Place_PaxNeverLowered guards the GREATEST semantics: a
// later order addressing only lower seats (or no seats at all) must leave an
// earlier, higher count intact — the people did not leave the table because
// the second round was for seat 1.
func TestOrderService_Place_PaxNeverLowered(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()

	c, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:   branchA,
		TableLabel: "Masa Seat-Lower",
		Pax:        5,
		OpenedBy:   &staffA,
	})
	require.NoError(t, err)

	place := func(seat int) {
		t.Helper()
		_, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
			BranchID:     branchA,
			CheckID:      &c.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Items:        []domain.OrderItem{seatItem(t, "Ayran", 1500, seat)},
		})
		require.NoError(t, err)
	}

	place(2)
	place(0)

	after, err := checks.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, after.Pax, "an order naming lower (or no) seats must never shrink pax")
}

// TestOrderService_Place_SeatOutOfRange pins the 0..domain.MaxSeatNo range:
// out-of-range values are rejected before any database work (ErrInvalidSeatNo
// → 422 invalid_seat_no at the HTTP layer), never left to surface as the
// CHECK constraint's opaque 500.
func TestOrderService_Place_SeatOutOfRange(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)

	for _, seat := range []int{-1, domain.MaxSeatNo + 1} {
		placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
			BranchID:     branchA,
			CheckID:      &c.ID,
			OrderChannel: domain.OrderChannelDineIn,
			Items:        []domain.OrderItem{seatItem(t, "Limonata", 2000, seat)},
		})
		assert.ErrorIs(t, err, service.ErrInvalidSeatNo, "seat %d", seat)
		assert.Equal(t, uuid.Nil, placed.ID, "a rejected order must not be persisted")
	}

	after, err := checks.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, after.Pax, "a rejected order must not have touched pax either")
}

// TestOrderService_Place_TakeawayCheck_PaxUntouched pins the service-type
// guard: seats on a takeaway (gel al) check's order are persisted — the
// client may still use them for grouping — but pax is meaningless off the
// floor plan and must not move.
func TestOrderService_Place_TakeawayCheck_PaxUntouched(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()

	c, err := checks.Open(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Check{
		BranchID:     branchA,
		ServiceType:  domain.ServiceTypeTakeaway,
		CustomerName: "Seat Takeaway",
		OpenedBy:     &staffA,
	})
	require.NoError(t, err)
	require.Equal(t, 1, c.Pax)

	placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		CheckID:      &c.ID,
		OrderChannel: domain.OrderChannelTakeaway,
		Items:        []domain.OrderItem{seatItem(t, "Tost", 4000, 3)},
	})
	require.NoError(t, err)
	require.Len(t, placed.Items, 1)
	assert.Equal(t, 3, placed.Items[0].SeatNo)

	after, err := checks.GetByID(ctx, tenantA, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, after.Pax, "pax on a takeaway check must never be derived from seats")
}

// TestOrderService_Place_NoCheck_SeatsStillPersist covers the masasız sale:
// an order without a check has no pax to derive, and the seat guard must not
// trip over the nil check.
func TestOrderService_Place_NoCheck_SeatsStillPersist(t *testing.T) {
	ctx := context.Background()
	orders := newOrderService()

	placed, err := orders.Place(chainWideCtx(t, ctx), tenantA, chainWidePrincipal(), domain.Order{
		BranchID:     branchA,
		OrderChannel: domain.OrderChannelTakeaway,
		Items:        []domain.OrderItem{seatItem(t, "Simit", 1200, 2)},
	})
	require.NoError(t, err)
	require.Len(t, placed.Items, 1)
	assert.Equal(t, 2, placed.Items[0].SeatNo)
}

// TestOrderService_PlaceGuest_SeatStaysZero pins the guest contract: the QR
// path has no seat concept (nobody numbers the chairs for an anonymous
// diner), so every guest line lands with seat_no 0 and the guest check's pax
// stays at its opening value of 1.
func TestOrderService_PlaceGuest_SeatStaysZero(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa Seat-QR")
	orders := newGuestOrderService()

	result, err := orders.PlaceGuest(ctx, guestRequestFor(table), nil)
	require.NoError(t, err)

	order, err := orders.GetByID(ctx, tenantA, result.OrderID)
	require.NoError(t, err)
	require.NotEmpty(t, order.Items)
	for i, it := range order.Items {
		assert.Equal(t, 0, it.SeatNo, "guest line %d must carry no seat", i)
	}

	check, err := newCheckService().GetByID(ctx, tenantA, result.CheckID)
	require.NoError(t, err)
	assert.Equal(t, 1, check.Pax, "a guest order must not move pax")
}
