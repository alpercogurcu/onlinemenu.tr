package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/modules/catalog/service"
)

func newProductService() *service.ProductService {
	return service.NewProductService(service.ProductParams{
		DB:          sharedPool,
		ProductRepo: repo.NewProductRepo(),
		Logger:      zap.NewNop(),
	})
}

// seedProductWithContractGaps creates a product carrying sku/image_key/barcode
// set — fields the PUT (and POST) request contract in http/handler.go never
// exposes to a client, so they can only be seeded here at the repo level.
func seedProductWithContractGaps(t *testing.T) (tenantID uuid.UUID, created domain.Product) {
	t.Helper()
	ctx := context.Background()
	tenantID = uuid.New()

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID:    tenantID,
			Name:        "Karışık Pizza",
			PriceAmount: 24000,
			Currency:    "TRY",
			Unit:        "adet",
			TaxRateBPS:  1000,
			IsActive:    true,
			SKU:         "SKU-1",
			ImageKey:    "img",
			Barcode:     "123",
		})
		return err
	}))
	return tenantID, created
}

// TestProductService_Update_PreservesFieldsOutsidePUTContract proves the C1
// fix: a PUT body built from the contract struct in http/handler.go (no
// sku/image_key/barcode fields, so they arrive as Go zero values) must not
// blank out what's already stored for those columns.
func TestProductService_Update_PreservesFieldsOutsidePUTContract(t *testing.T) {
	tenantID, created := seedProductWithContractGaps(t)
	svc := newProductService()

	// Mirrors what updateProduct (http/handler.go) builds from its decode
	// struct: every PUT-contract field set, sku/image_key/barcode absent.
	updated, err := svc.Update(context.Background(), tenantID, domain.Product{
		ID:                created.ID,
		CategoryID:        created.CategoryID,
		Name:              "Karışık Pizza XL",
		PriceAmount:       27000,
		Currency:          "TRY",
		Unit:              "adet",
		TaxRateBPS:        1000,
		IsActive:          true,
		SourceStockItemID: nil,
	})
	require.NoError(t, err)

	assert.Equal(t, "SKU-1", updated.SKU, "sku is outside the PUT contract and must be preserved")
	assert.Equal(t, "img", updated.ImageKey, "image_key is outside the PUT contract and must be preserved")
	assert.Equal(t, "123", updated.Barcode, "barcode is outside the PUT contract and must be preserved")
	assert.Equal(t, "Karışık Pizza XL", updated.Name)
	assert.Equal(t, int64(27000), updated.PriceAmount)

	// Re-fetch to make sure the preserved values were actually persisted,
	// not just carried on the in-memory return value.
	var fetched domain.Product
	require.NoError(t, sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		var err error
		fetched, err = repo.NewProductRepo().GetByID(context.Background(), tx, created.ID)
		return err
	}))
	assert.Equal(t, "SKU-1", fetched.SKU)
	assert.Equal(t, "img", fetched.ImageKey)
	assert.Equal(t, "123", fetched.Barcode)
}

// TestProductService_Update_EmptyCurrencyFallsBackToExisting proves an empty
// currency in the PUT body (CHAR(3) NOT NULL column) does not get written
// as-is (which would persist as "   "), but falls back to the existing value.
func TestProductService_Update_EmptyCurrencyFallsBackToExisting(t *testing.T) {
	tenantID, created := seedProductWithContractGaps(t)
	svc := newProductService()

	updated, err := svc.Update(context.Background(), tenantID, domain.Product{
		ID:          created.ID,
		Name:        created.Name,
		PriceAmount: created.PriceAmount,
		Currency:    "",
		Unit:        created.Unit,
		TaxRateBPS:  created.TaxRateBPS,
		IsActive:    created.IsActive,
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", updated.Currency, "empty currency must fall back to the existing value")
}

// TestProductService_Update_OmittedSourceStockItemIDClearsLink rules on the
// ambiguous case explicitly: the admin now always sends source_stock_item_id
// as part of the PUT contract (it is not an omitted-field gap like sku/
// image_key/barcode), so a nil value in the request body is a deliberate
// "unlink" and must clear any existing link rather than being ignored.
func TestProductService_Update_OmittedSourceStockItemIDClearsLink(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	stockItemID := uuid.New()
	svc := newProductService()

	var created domain.Product
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		created, err = repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID:          tenantID,
			Name:              "Stoktan Satılan Ürün",
			PriceAmount:       9000,
			Currency:          "TRY",
			Unit:              "adet",
			IsActive:          true,
			SourceStockItemID: &stockItemID,
		})
		return err
	}))
	require.NotNil(t, created.SourceStockItemID)

	// PUT with source_stock_item_id present: link persists.
	updated, err := svc.Update(ctx, tenantID, domain.Product{
		ID:                created.ID,
		Name:              created.Name,
		PriceAmount:       created.PriceAmount,
		Currency:          created.Currency,
		Unit:              created.Unit,
		IsActive:          created.IsActive,
		SourceStockItemID: &stockItemID,
	})
	require.NoError(t, err)
	require.NotNil(t, updated.SourceStockItemID)
	assert.Equal(t, stockItemID, *updated.SourceStockItemID)

	// PUT omitting source_stock_item_id (nil): link is cleared.
	updated, err = svc.Update(ctx, tenantID, domain.Product{
		ID:                created.ID,
		Name:              created.Name,
		PriceAmount:       created.PriceAmount,
		Currency:          created.Currency,
		Unit:              created.Unit,
		IsActive:          created.IsActive,
		SourceStockItemID: nil,
	})
	require.NoError(t, err)
	assert.Nil(t, updated.SourceStockItemID, "an omitted source_stock_item_id must clear the existing link")
}
