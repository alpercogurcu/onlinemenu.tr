package service_test

// PriceStaffCart is the POS's half of the server-side price check
// (docs/pos-ux-spec.md bulgu #14 / P0). It shares its whole body with
// PriceCart except for where the base price comes from, so what is worth
// testing separately is exactly that difference — plus proof that the shared
// modifier rules really do apply on this path too.

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

func TestPriceStaffCart_AppliesModifierDeltasOnce(t *testing.T) {
	f := seedPricingFixture(t)

	priced, err := newPricingService().PriceStaffCart(context.Background(), f.tenantID, f.branchID,
		[]pub.StaffCartLine{{ProductID: f.productID, Quantity: 2, ModifierIDs: []uuid.UUID{f.extraID, f.sizeLarge}}})
	require.NoError(t, err)
	require.Len(t, priced, 1)

	assert.Equal(t, int64(15000), priced[0].BasePriceAmount)
	assert.Equal(t, int64(15000+2000+3000), priced[0].UnitPriceAmount,
		"the deltas land in the unit price, because pos bills quantity × unit_price_amount")
	assert.Equal(t, 2, priced[0].Quantity)
	assert.Equal(t, 1000, priced[0].TaxRateBPS)
}

// TestPriceStaffCart_PricesProductsWithNoMenu is the whole reason this method
// exists beside PriceCart: a counter sale must not depend on the branch
// having an active menu. A tenant that never configured one would otherwise
// be unable to take any POS order at all once the price check went live.
func TestPriceStaffCart_PricesProductsWithNoMenu(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	var productID uuid.UUID

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID: tenantID, Name: "Menüsüz Ürün", PriceAmount: 7500,
			Currency: "TRY", Unit: "adet", TaxRateBPS: 100, IsActive: true,
		})
		productID = p.ID
		return err
	}))

	svc := newPricingService()

	priced, err := svc.PriceStaffCart(ctx, tenantID, uuid.Nil, []pub.StaffCartLine{{ProductID: productID, Quantity: 1}})
	require.NoError(t, err)
	require.Len(t, priced, 1)
	assert.Equal(t, int64(7500), priced[0].UnitPriceAmount)

	// The diner-facing path still refuses it — that asymmetry is the point.
	_, err = svc.PriceCart(ctx, tenantID, uuid.New(), []pub.CartLine{{ProductID: productID, Quantity: 1}})
	var invalid *pub.ValidationError
	assert.ErrorAs(t, err, &invalid, "a product on no menu is not orderable by a guest")
}

func TestPriceStaffCart_RejectsForeignAndRepeatedModifiers(t *testing.T) {
	f := seedPricingFixture(t)
	svc := newPricingService()
	ctx := context.Background()

	cases := []struct {
		name string
		line pub.StaffCartLine
	}{
		{"modifier not attached to the product", pub.StaffCartLine{
			ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{uuid.New()},
		}},
		{"same modifier twice", pub.StaffCartLine{
			ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{f.discountID, f.discountID},
		}},
		{"two options from a single-selection group", pub.StaffCartLine{
			ProductID: f.productID, Quantity: 1, ModifierIDs: []uuid.UUID{f.sizeSmall, f.sizeLarge},
		}},
		{"unknown product", pub.StaffCartLine{ProductID: uuid.New(), Quantity: 1}},
		{"non-positive quantity", pub.StaffCartLine{ProductID: f.productID, Quantity: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.PriceStaffCart(ctx, f.tenantID, f.branchID, []pub.StaffCartLine{tc.line})
			var invalid *pub.ValidationError
			assert.ErrorAs(t, err, &invalid)
		})
	}
}

// TestPriceStaffCart_RejectsInactiveProduct proves a delisted product cannot
// be sold at the counter either: PriceCatalogProducts filters on is_active,
// so the line has no price to come back with.
func TestPriceStaffCart_RejectsInactiveProduct(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	var productID uuid.UUID

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID: tenantID, Name: "Kaldırılmış", PriceAmount: 1000,
			Currency: "TRY", Unit: "adet", TaxRateBPS: 100, IsActive: false,
		})
		productID = p.ID
		return err
	}))

	_, err := newPricingService().PriceStaffCart(ctx, tenantID, uuid.Nil,
		[]pub.StaffCartLine{{ProductID: productID, Quantity: 1}})
	var invalid *pub.ValidationError
	assert.ErrorAs(t, err, &invalid)
}
