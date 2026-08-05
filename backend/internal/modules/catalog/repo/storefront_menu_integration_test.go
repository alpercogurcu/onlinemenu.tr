package repo_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/repo"
)

// These tests run the storefront read model against a real Postgres (shared
// TestMain container). The queries carry the whole "what may a diner see and
// at what price" decision, so their correctness cannot be asserted against
// stubs: a wrong join or a drifting CTE compiles and unit-tests fine, and only
// a database can say whether it returns the right rows.

type storefrontFixture struct {
	tenantID  uuid.UUID
	branchID  uuid.UUID
	otherID   uuid.UUID // a second branch of the same tenant
	category  domain.Category
	productID uuid.UUID
	menuID    uuid.UUID
}

// seedStorefrontMenu builds a minimal but complete menu: one category, one
// product, one branch-wide active menu carrying it at a price override.
func seedStorefrontMenu(t *testing.T, priceOverride *int64) storefrontFixture {
	t.Helper()
	ctx := context.Background()

	f := storefrontFixture{
		tenantID: uuid.New(),
		branchID: uuid.New(),
		otherID:  uuid.New(),
	}

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		cat, err := repo.NewCategoryRepo().Create(ctx, tx, domain.Category{
			TenantID: f.tenantID, Name: "Ana Yemekler", IsActive: true, SortOrder: 1,
		})
		if err != nil {
			return err
		}
		f.category = cat

		product, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID:    f.tenantID,
			CategoryID:  &cat.ID,
			Name:        "Adana Kebap",
			Description: "Acılı",
			PriceAmount: 18000,
			Currency:    "TRY",
			Unit:        "porsiyon",
			TaxRateBPS:  1000,
			IsActive:    true,
			ImageKey:    "products/adana.jpg",
		})
		if err != nil {
			return err
		}
		f.productID = product.ID

		menu, err := repo.NewMenuRepo().Create(ctx, tx, domain.Menu{
			TenantID: f.tenantID, BranchID: &f.branchID, Name: "Akşam Menüsü", IsActive: true,
		})
		if err != nil {
			return err
		}
		f.menuID = menu.ID

		return repo.NewMenuItemRepo().AddItem(ctx, tx, domain.MenuItem{
			MenuID: menu.ID, ProductID: product.ID, TenantID: f.tenantID,
			PriceOverride: priceOverride, IsActive: true,
		})
	}))
	return f
}

func TestStorefrontMenuRepo_ListMenuRows_UsesMenuPriceOverride(t *testing.T) {
	ctx := context.Background()
	override := int64(15000)
	f := seedStorefrontMenu(t, &override)
	r := repo.NewStorefrontMenuRepo()

	var rows []repo.StorefrontMenuRow
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		return err
	}))

	require.Len(t, rows, 1)
	assert.Equal(t, f.productID, rows[0].ProductID)
	assert.Equal(t, f.category.ID, rows[0].CategoryID)
	assert.Equal(t, "Ana Yemekler", rows[0].CategoryName)
	assert.Equal(t, int64(15000), rows[0].PriceAmount, "menu_items.price_override wins over products.price_amount")
	assert.Equal(t, "products/adana.jpg", rows[0].ImageKey)
	assert.True(t, rows[0].IsAvailable, "no product_channel_availability row means available (opt-out semantics)")
}

func TestStorefrontMenuRepo_ListMenuRows_FallsBackToProductPrice(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	var rows []repo.StorefrontMenuRow
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		return err
	}))

	require.Len(t, rows, 1)
	assert.Equal(t, int64(18000), rows[0].PriceAmount)
}

// TestStorefrontMenuRepo_OtherBranchSeesNothing: a branch-scoped menu must not
// bleed into another branch of the same tenant (RLS only isolates tenants).
func TestStorefrontMenuRepo_OtherBranchSeesNothing(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	var rows []repo.StorefrontMenuRow
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.otherID)
		return err
	}))

	assert.Empty(t, rows)
}

// TestStorefrontMenuRepo_TenantIsolation: another tenant's read tx sees no
// row at all, even with the correct branch id in hand.
func TestStorefrontMenuRepo_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	var rows []repo.StorefrontMenuRow
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, uuid.New(), func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		return err
	}))

	assert.Empty(t, rows)
}

// TestStorefrontMenuRepo_BranchMenuBeatsTenantWideMenu pins the tie-break the
// browse and re-price queries share: when two active menus carry the same
// product, the branch-specific one wins — deterministically, or the server
// could charge a price the diner never saw.
func TestStorefrontMenuRepo_BranchMenuBeatsTenantWideMenu(t *testing.T) {
	ctx := context.Background()
	override := int64(15000)
	f := seedStorefrontMenu(t, &override)
	r := repo.NewStorefrontMenuRepo()

	tenantWide := int64(9900)
	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		menu, err := repo.NewMenuRepo().Create(ctx, tx, domain.Menu{
			TenantID: f.tenantID, Name: "Zincir Geneli", IsActive: true,
		})
		if err != nil {
			return err
		}
		return repo.NewMenuItemRepo().AddItem(ctx, tx, domain.MenuItem{
			MenuID: menu.ID, ProductID: f.productID, TenantID: f.tenantID,
			PriceOverride: &tenantWide, IsActive: true,
		})
	}))

	var rows []repo.StorefrontMenuRow
	var priced map[uuid.UUID]repo.PricedProduct
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		if err != nil {
			return err
		}
		priced, err = r.PriceProducts(ctx, tx, f.branchID, []uuid.UUID{f.productID})
		return err
	}))

	require.Len(t, rows, 1, "a product on two menus must appear exactly once")
	assert.Equal(t, int64(15000), rows[0].PriceAmount)
	assert.Equal(t, int64(15000), priced[f.productID].PriceAmount,
		"browse and re-price must resolve the SAME menu, or the diner is charged a price they never saw")
}

func TestStorefrontMenuRepo_InactiveMenuItemIsHidden(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		return repo.NewMenuItemRepo().AddItem(ctx, tx, domain.MenuItem{
			MenuID: f.menuID, ProductID: f.productID, TenantID: f.tenantID, IsActive: false,
		})
	}))

	var rows []repo.StorefrontMenuRow
	var priced map[uuid.UUID]repo.PricedProduct
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		if err != nil {
			return err
		}
		priced, err = r.PriceProducts(ctx, tx, f.branchID, []uuid.UUID{f.productID})
		return err
	}))

	assert.Empty(t, rows)
	assert.Empty(t, priced, "a product hidden from the menu must also be unpriceable")
}

// TestStorefrontMenuRepo_DineInClosedProductIsUnpriceable proves the opt-out
// availability semantics: only an explicit is_available = FALSE hides it.
func TestStorefrontMenuRepo_DineInClosedProductIsUnpriceable(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO product_channel_availability (tenant_id, product_id, order_channel, integrator_slug, is_available)
			VALUES ($1, $2, 'dine_in', NULL, FALSE)`, f.tenantID, f.productID)
		return err
	}))

	var rows []repo.StorefrontMenuRow
	var priced map[uuid.UUID]repo.PricedProduct
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		if err != nil {
			return err
		}
		priced, err = r.PriceProducts(ctx, tx, f.branchID, []uuid.UUID{f.productID})
		return err
	}))

	require.Len(t, rows, 1)
	assert.False(t, rows[0].IsAvailable, "the menu still lists it, greyed out")
	assert.Empty(t, priced, "but it cannot be ordered")
}

func TestStorefrontMenuRepo_ModifiersAndPricing(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	var (
		group    domain.ModifierGroup
		modifier domain.Modifier
	)
	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		group, err = repo.NewModifierGroupRepo().Create(ctx, tx, domain.ModifierGroup{
			TenantID: f.tenantID, Name: "Ekstralar", SelectionType: "multiple",
		})
		if err != nil {
			return err
		}
		modifier, err = repo.NewModifierRepo().Create(ctx, tx, domain.Modifier{
			TenantID: f.tenantID, GroupID: group.ID, Name: "Ekstra Sos", PriceDelta: 500, IsActive: true,
		})
		if err != nil {
			return err
		}
		return repo.NewProductModifierGroupRepo().Assign(ctx, tx, f.productID, group.ID, f.tenantID, 0)
	}))

	var (
		modRows   []repo.StorefrontModifierRow
		attached  []repo.ProductModifier
		unrelated []repo.ProductModifier
	)
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		modRows, err = r.ListModifierRows(ctx, tx, []uuid.UUID{f.productID})
		if err != nil {
			return err
		}
		attached, err = r.PriceModifiers(ctx, tx, []uuid.UUID{f.productID}, []uuid.UUID{modifier.ID})
		if err != nil {
			return err
		}
		// A modifier that exists but is attached to no such product must not
		// price a line — the pair is what is verified, not the modifier alone.
		unrelated, err = r.PriceModifiers(ctx, tx, []uuid.UUID{uuid.New()}, []uuid.UUID{modifier.ID})
		return err
	}))

	require.Len(t, modRows, 1)
	assert.Equal(t, group.ID, modRows[0].GroupID)
	assert.Equal(t, "Ekstra Sos", modRows[0].ModifierName)
	assert.Equal(t, int64(500), modRows[0].PriceDelta)

	require.Len(t, attached, 1)
	assert.Equal(t, f.productID, attached[0].ProductID)
	assert.Equal(t, group.ID, attached[0].GroupID,
		"the group's selection rule must travel with the modifier — it is the only bound on stacking options")
	assert.Equal(t, "multiple", attached[0].SelectionType)
	assert.Empty(t, unrelated)
}

func TestStorefrontMenuRepo_UncategorisedProductStillListed(t *testing.T) {
	ctx := context.Background()
	f := seedStorefrontMenu(t, nil)
	r := repo.NewStorefrontMenuRepo()

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE products SET category_id = NULL WHERE id = $1`, f.productID)
		return err
	}))

	var rows []repo.StorefrontMenuRow
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = r.ListMenuRows(ctx, tx, f.branchID)
		return err
	}))

	require.Len(t, rows, 1, "a sellable product must not vanish because its category did")
	assert.Equal(t, uuid.Nil, rows[0].CategoryID)
	assert.Empty(t, rows[0].CategoryName)
}
