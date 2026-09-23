package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/auth"
)

func countRows(t *testing.T, tenantID uuid.UUID, table string, productID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, sharedPool.WithTenantReadTx(context.Background(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE product_id = $1", productID).Scan(&n)
	}))
	return n
}

func TestProductService_Delete_RemovesMenuItemsOverridesAndGroupLinks(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	branchID := uuid.New()
	menu := seedMenu(t, tenantID, "Ana Menü", true)
	svc := newProductService()

	p, err := svc.Create(ctx, tenantID, domain.Product{Name: "Silinecek", PriceAmount: 1000, Currency: "TRY", Unit: "adet", IsActive: true})
	require.NoError(t, err)
	require.Contains(t, menuProductIDs(t, tenantID, menu.ID), p.ID)

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		g, err := repo.NewModifierGroupRepo().Create(ctx, tx, domain.ModifierGroup{
			TenantID: tenantID, Name: "Boy", SelectionType: domain.SelectionSingle,
		})
		if err != nil {
			return err
		}
		if err := repo.NewProductModifierGroupRepo().Assign(ctx, tx, p.ID, g.ID, tenantID, 0); err != nil {
			return err
		}
		price := int64(1500)
		_, err = repo.NewBranchOverrideRepo().Upsert(ctx, tx, domain.BranchProductOverride{
			TenantID: tenantID, BranchID: branchID, ProductID: p.ID, IsAvailable: true, PriceAmount: &price,
		})
		return err
	}))

	require.NoError(t, svc.Delete(ctx, tenantID, p.ID))

	assert.NotContains(t, menuProductIDs(t, tenantID, menu.ID), p.ID)
	assert.Zero(t, countRows(t, tenantID, "menu_items", p.ID))
	assert.Zero(t, countRows(t, tenantID, "product_modifier_groups", p.ID))
	assert.Zero(t, countRows(t, tenantID, "branch_product_overrides", p.ID))
}

func TestCatalogHTTP_EmptyIDListsAreArrays(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	svc := newProductService()
	p, err := svc.Create(ctx, tenantID, domain.Product{Name: "Silinecek", PriceAmount: 1000, Currency: "TRY", Unit: "adet", IsActive: true})
	require.NoError(t, err)

	var groupID uuid.UUID
	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		g, err := repo.NewModifierGroupRepo().Create(ctx, tx, domain.ModifierGroup{
			TenantID: tenantID, Name: "Boş Grup", SelectionType: domain.SelectionSingle,
		})
		groupID = g.ID
		return err
	}))

	h := cataloghttp.NewHandler(cataloghttp.Params{
		Products:        svc,
		Modifiers:       newModifierService(),
		BranchOverrides: newBranchOverrideService(),
		Logger:          zap.NewNop(),
		Engine:          testEngine(t),
	})
	mux := chi.NewMux()
	h.RegisterRoutes(mux)

	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
			PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: tenantID,
			RoleIDs: []uuid.UUID{catalogManagerRoleID},
		}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/api/v1/catalog/products/"+p.ID.String()).Code)
	require.Equal(t, http.StatusNoContent, do(http.MethodDelete, "/api/v1/catalog/products/"+p.ID.String()).Code)
	// Soft delete and "deactivate" share is_active, and the admin editor loads
	// a deactivated product by id to reactivate it, so GET must keep answering.
	assert.Equal(t, http.StatusOK, do(http.MethodGet, "/api/v1/catalog/products/"+p.ID.String()).Code)

	rec := do(http.MethodGet, "/api/v1/catalog/modifier-groups/"+groupID.String()+"/products")
	require.Equal(t, http.StatusOK, rec.Code)
	var raw json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.JSONEq(t, "[]", string(raw))

	rec = do(http.MethodGet, "/api/v1/catalog/products/"+p.ID.String()+"/modifier-groups")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}
