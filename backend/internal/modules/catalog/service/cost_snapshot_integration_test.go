package service_test

// Unit cost resolution (branch override -> product -> nil). The assertions
// that matter are the NULL ones: an unknown cost must reach pos as nil, never
// as 0, or profit reports would count unknown-cost sales as free.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/catalog/domain"
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
)

func setProductCost(t *testing.T, tenantID, productID uuid.UUID, cost *int64) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := repo.NewProductRepo().GetByID(ctx, tx, productID)
		if err != nil {
			return err
		}
		p.CostAmount = cost
		_, err = repo.NewProductRepo().Update(ctx, tx, p)
		return err
	}))
}

func staffCost(t *testing.T, tenantID, branchID, productID uuid.UUID) *int64 {
	t.Helper()
	priced, err := newPricingService().PriceStaffCart(context.Background(), tenantID, branchID,
		[]pub.StaffCartLine{{ProductID: productID, Quantity: 1}})
	require.NoError(t, err)
	require.Len(t, priced, 1)
	return priced[0].UnitCostAmount
}

func TestStaffPricing_UnitCostResolution(t *testing.T) {
	f := seedOverrideFixture(t)
	ctx := context.Background()
	productCost, branchCost, zero := int64(11000), int64(13000), int64(0)

	require.Nil(t, staffCost(t, f.tenantID, f.branchB, f.kebap), "cost unknown stays nil, not 0")

	setProductCost(t, f.tenantID, f.kebap, &productCost)
	require.NotNil(t, staffCost(t, f.tenantID, f.branchB, f.kebap))
	assert.Equal(t, productCost, *staffCost(t, f.tenantID, f.branchB, f.kebap), "product cost without override")

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, CostAmount: &branchCost,
	})
	require.NoError(t, err)
	assert.Equal(t, branchCost, *staffCost(t, f.tenantID, f.branchB, f.kebap), "branch cost wins")
	assert.Equal(t, productCost, *staffCost(t, f.tenantID, f.branchA, f.kebap), "other branch keeps product cost")

	_, err = newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, CostAmount: &zero,
	})
	require.NoError(t, err)
	require.NotNil(t, staffCost(t, f.tenantID, f.branchB, f.kebap))
	assert.Equal(t, int64(0), *staffCost(t, f.tenantID, f.branchB, f.kebap), "an explicit 0 cost is a real value")

	_, err = newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.ayran, IsAvailable: true,
	})
	require.NoError(t, err)
	assert.Nil(t, staffCost(t, f.tenantID, f.branchB, f.ayran), "override row without cost and no product cost is nil")
}

func TestGuestPricing_UnitCostFollowsBranchOverride(t *testing.T) {
	ctx := context.Background()
	f := seedPricingFixture(t)
	productCost, branchCost := int64(6000), int64(7000)

	cartCost := func() *int64 {
		priced, err := newPricingService().PriceCart(ctx, f.tenantID, f.branchID,
			[]pub.CartLine{{ProductID: f.productID, Quantity: 1}})
		require.NoError(t, err)
		require.Len(t, priced, 1)
		return priced[0].UnitCostAmount
	}

	require.Nil(t, cartCost())
	setProductCost(t, f.tenantID, f.productID, &productCost)
	require.NotNil(t, cartCost())
	assert.Equal(t, productCost, *cartCost())

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchID, ProductID: f.productID, IsAvailable: true, CostAmount: &branchCost,
	})
	require.NoError(t, err)
	assert.Equal(t, branchCost, *cartCost())
}

func TestCostAmount_NegativeRejected(t *testing.T) {
	f := seedOverrideFixture(t)
	neg := int64(-1)
	_, err := newBranchOverrideService().Upsert(context.Background(), f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, CostAmount: &neg,
	})
	var invalid *pub.ValidationError
	assert.ErrorAs(t, err, &invalid)
}
