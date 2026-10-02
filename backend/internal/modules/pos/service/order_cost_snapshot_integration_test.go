package service_test

// The cost snapshot (pos/000013): what the catalog resolved at placement is
// what order_items keeps, and "unknown" survives as NULL instead of 0.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderService_Place_SnapshotsUnitCost(t *testing.T) {
	ctx := context.Background()
	checks := newCheckService()
	orders := newOrderService()
	c := openTestCheck(t, ctx, checks)
	cost := int64(1250)
	zero := int64(0)

	tests := []struct {
		name string
		cost *int64
		want *int64
	}{
		{"known cost is stored", &cost, &cost},
		{"zero cost stays zero", &zero, &zero},
		{"unknown cost stays NULL", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := testProductWithCost("Maliyetli", 4500, tt.cost)
			placed, err := placeWithPrice(t, ctx, orders, c.ID, product, 4500)
			require.NoError(t, err)

			stored, err := orders.GetByID(ctx, tenantA, placed.ID)
			require.NoError(t, err)
			require.Len(t, stored.Items, 1)
			if tt.want == nil {
				assert.Nil(t, stored.Items[0].UnitCostAmount)
				return
			}
			require.NotNil(t, stored.Items[0].UnitCostAmount)
			assert.Equal(t, *tt.want, *stored.Items[0].UnitCostAmount)
		})
	}
}

func TestOrderService_PlaceGuest_SnapshotsUnitCost(t *testing.T) {
	ctx := context.Background()
	table := seedGuestTable(t, "Masa Cost-QR")
	orders := newGuestOrderService()
	cost := int64(900)

	req := guestRequestFor(table)
	req.Lines = append(req.Lines, req.Lines[0])
	req.Lines[0].UnitCostAmount = &cost

	result, err := orders.PlaceGuest(ctx, req, nil)
	require.NoError(t, err)

	stored, err := orders.GetByID(ctx, tenantA, result.OrderID)
	require.NoError(t, err)
	require.Len(t, stored.Items, 2)

	var withCost, withoutCost int
	for _, it := range stored.Items {
		if it.UnitCostAmount == nil {
			withoutCost++
			continue
		}
		assert.Equal(t, cost, *it.UnitCostAmount)
		withCost++
	}
	assert.Equal(t, 1, withCost)
	assert.Equal(t, 1, withoutCost, "unknown cost must stay NULL, not 0")
}
