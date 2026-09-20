package service_test

// GET /api/v1/catalog/categories/{id}/products: the POS product picker reads
// this route, and POST /pos/orders rejects inactive products with
// invalid_order_line, so by default the route must hide them. The admin
// keeps a way to see everything with ?include_inactive=true. The whole chain
// runs for real: chi route, embedded OPA, ProductService against Postgres.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/catalog/domain"
	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/platform/auth"
)

var catalogManagerRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000006")

func categoryProductsMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)

	h := cataloghttp.NewHandler(cataloghttp.Params{
		Products: newProductService(),
		// The listing folds branch overrides in even when no branch_id is
		// given (it short-circuits), so the service must be wired or the
		// handler nil-panics on the tenant-wide path too.
		BranchOverrides: newBranchOverrideService(),
		Logger:          zap.NewNop(),
		Engine:          engine,
	})
	mux := chi.NewMux()
	h.RegisterRoutes(mux)
	return mux
}

func getCategoryProducts(t *testing.T, mux *chi.Mux, tenantID, categoryID uuid.UUID, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/categories/"+categoryID.String()+"/products"+query, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		RoleIDs:  []uuid.UUID{catalogManagerRoleID},
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func seedCategoryWithActiveAndInactiveProduct(t *testing.T) (tenantID, categoryID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tenantID = uuid.New()

	require.NoError(t, sharedPool.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		cat, err := repo.NewCategoryRepo().Create(ctx, tx, domain.Category{TenantID: tenantID, Name: "Izgara", IsActive: true})
		if err != nil {
			return err
		}
		categoryID = cat.ID
		for _, p := range []domain.Product{
			{Name: "Adana Kebap", IsActive: true, SortOrder: 1},
			{Name: "Kaldırılan Ürün", IsActive: false, SortOrder: 2},
		} {
			p.TenantID = tenantID
			p.CategoryID = &cat.ID
			p.PriceAmount = 32000
			p.Currency = "TRY"
			p.Unit = "porsiyon"
			p.TaxRateBPS = 1000
			if _, err := repo.NewProductRepo().Create(ctx, tx, p); err != nil {
				return err
			}
		}
		return nil
	}))
	return tenantID, categoryID
}

func productNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got []struct {
		Name     string `json:"name"`
		IsActive bool   `json:"is_active"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	return names
}

func TestCategoryProductsHTTP_DefaultHidesInactive(t *testing.T) {
	mux := categoryProductsMux(t)
	tenantID, categoryID := seedCategoryWithActiveAndInactiveProduct(t)

	rec := getCategoryProducts(t, mux, tenantID, categoryID, "")

	assert.Equal(t, []string{"Adana Kebap"}, productNames(t, rec))
}

func TestCategoryProductsHTTP_IncludeInactiveRestoresFullList(t *testing.T) {
	mux := categoryProductsMux(t)
	tenantID, categoryID := seedCategoryWithActiveAndInactiveProduct(t)

	rec := getCategoryProducts(t, mux, tenantID, categoryID, "?include_inactive=true")

	assert.Equal(t, []string{"Adana Kebap", "Kaldırılan Ürün"}, productNames(t, rec))
}

func TestCategoryProductsHTTP_IncludeInactiveFalseIsTheDefault(t *testing.T) {
	mux := categoryProductsMux(t)
	tenantID, categoryID := seedCategoryWithActiveAndInactiveProduct(t)

	rec := getCategoryProducts(t, mux, tenantID, categoryID, "?include_inactive=false")

	assert.Equal(t, []string{"Adana Kebap"}, productNames(t, rec))
}

// A typo must not silently fall back to one of the two behaviours: a client
// that meant "everything" and got the active list would hide products.
func TestCategoryProductsHTTP_InvalidIncludeInactiveIs400(t *testing.T) {
	mux := categoryProductsMux(t)
	tenantID, categoryID := seedCategoryWithActiveAndInactiveProduct(t)

	rec := getCategoryProducts(t, mux, tenantID, categoryID, "?include_inactive=maybe")

	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}
