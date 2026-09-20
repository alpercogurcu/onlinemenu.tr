package service_test

// Branch product overrides (ADR-DATA-009). What is worth proving here is not
// that a row can be written — it is that the row changes what the SERVER
// charges, on every path that can take money: the counter sale
// (PriceStaffCart), the guest cart (PriceCart) and the product listing the
// cashier reads prices from. A test that only asserted the CRUD would pass
// while a branch sold at the tenant price.

import (
	"bytes"
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
	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/modules/catalog/repo"
	"onlinemenu.tr/internal/modules/catalog/service"
	"onlinemenu.tr/internal/platform/auth"
)

var catalogCashierRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000001")

func newBranchOverrideService() *service.BranchOverrideService {
	return service.NewBranchOverrideService(service.BranchOverrideParams{
		DB:           sharedPool,
		OverrideRepo: repo.NewBranchOverrideRepo(),
		ProductRepo:  repo.NewProductRepo(),
		Logger:       zap.NewNop(),
	})
}

// overrideFixture is one tenant, two branches and two products, all priced at
// the tenant default — the state every assertion below starts from.
type overrideFixture struct {
	tenantID uuid.UUID
	branchA  uuid.UUID
	branchB  uuid.UUID
	category uuid.UUID
	kebap    uuid.UUID
	ayran    uuid.UUID
}

const (
	kebapTenantPrice = int64(32000)
	ayranTenantPrice = int64(4000)
	kebapBranchPrice = int64(37000)
)

func seedOverrideFixture(t *testing.T) overrideFixture {
	t.Helper()
	ctx := context.Background()
	f := overrideFixture{tenantID: uuid.New(), branchA: uuid.New(), branchB: uuid.New()}

	require.NoError(t, sharedPool.WithTenantTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		cat, err := repo.NewCategoryRepo().Create(ctx, tx, domain.Category{
			TenantID: f.tenantID, Name: "Ana Yemekler", IsActive: true,
		})
		if err != nil {
			return err
		}
		f.category = cat.ID

		kebap, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID: f.tenantID, CategoryID: &cat.ID, Name: "Adana Kebap",
			PriceAmount: kebapTenantPrice, Currency: "TRY", Unit: "porsiyon",
			TaxRateBPS: 1000, IsActive: true, SortOrder: 1,
		})
		if err != nil {
			return err
		}
		f.kebap = kebap.ID

		ayran, err := repo.NewProductRepo().Create(ctx, tx, domain.Product{
			TenantID: f.tenantID, CategoryID: &cat.ID, Name: "Ayran",
			PriceAmount: ayranTenantPrice, Currency: "TRY", Unit: "adet",
			TaxRateBPS: 100, IsActive: true, SortOrder: 2,
		})
		if err != nil {
			return err
		}
		f.ayran = ayran.ID
		return nil
	}))
	return f
}

func priceOf(t *testing.T, tenantID, branchID, productID uuid.UUID) int64 {
	t.Helper()
	priced, err := newPricingService().PriceStaffCart(context.Background(), tenantID, branchID,
		[]pub.StaffCartLine{{ProductID: productID, Quantity: 1}})
	require.NoError(t, err)
	require.Len(t, priced, 1)
	return priced[0].UnitPriceAmount
}

// TestBranchOverride_StaffPriceIsBranchEffective is the whole point of the
// feature: the SAME product, the SAME tenant, two branches, two prices — and
// the branch without an override must keep the tenant price rather than
// inheriting its sibling's.
func TestBranchOverride_StaffPriceIsBranchEffective(t *testing.T) {
	f := seedOverrideFixture(t)
	svc := newBranchOverrideService()
	price := kebapBranchPrice

	_, err := svc.Upsert(context.Background(), f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)

	assert.Equal(t, kebapBranchPrice, priceOf(t, f.tenantID, f.branchB, f.kebap), "override'lı şube kendi fiyatını uygular")
	assert.Equal(t, kebapTenantPrice, priceOf(t, f.tenantID, f.branchA, f.kebap), "override'sız şube tenant fiyatında kalır")
	assert.Equal(t, kebapTenantPrice, priceOf(t, f.tenantID, uuid.Nil, f.kebap), "şube verilmediğinde tenant varsayılanı")
	assert.Equal(t, ayranTenantPrice, priceOf(t, f.tenantID, f.branchB, f.ayran), "override edilmeyen ürün etkilenmez")
}

// An override row with no price only toggles availability: it must not reset
// the product to 0 kuruş, which a non-pointer column would have done.
func TestBranchOverride_NullPriceKeepsTenantPrice(t *testing.T) {
	f := seedOverrideFixture(t)

	_, err := newBranchOverrideService().Upsert(context.Background(), f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: nil,
	})
	require.NoError(t, err)

	assert.Equal(t, kebapTenantPrice, priceOf(t, f.tenantID, f.branchB, f.kebap))
}

// TestBranchOverride_UnavailableProductIsNotOrderable is the 422 the POS
// surfaces as invalid_order_line: a branch that does not sell a product must
// not be able to sell it at the tenant price by sending the id directly.
func TestBranchOverride_UnavailableProductIsNotOrderable(t *testing.T) {
	f := seedOverrideFixture(t)
	ctx := context.Background()

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.ayran, IsAvailable: false,
	})
	require.NoError(t, err)

	_, err = newPricingService().PriceStaffCart(ctx, f.tenantID, f.branchB,
		[]pub.StaffCartLine{{ProductID: f.ayran, Quantity: 1}})
	var invalid *pub.ValidationError
	assert.ErrorAs(t, err, &invalid, "şubede kapalı ürün sipariş edilemez")

	// The other branch is untouched — availability is per branch, not a
	// disguised second is_active flag on the product.
	assert.Equal(t, ayranTenantPrice, priceOf(t, f.tenantID, f.branchA, f.ayran))
}

// TestBranchOverride_GuestCartUsesBranchPrice proves the storefront half:
// PriceCart resolves the override through the same CTE the guest menu reads,
// so a diner is charged what the branch's menu showed them.
func TestBranchOverride_GuestCartUsesBranchPrice(t *testing.T) {
	ctx := context.Background()
	f := seedPricingFixture(t) // product on an active branch menu, 15000 kuruş
	price := int64(21000)

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchID, ProductID: f.productID, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)

	priced, err := newPricingService().PriceCart(ctx, f.tenantID, f.branchID,
		[]pub.CartLine{{ProductID: f.productID, Quantity: 1}})
	require.NoError(t, err)
	require.Len(t, priced, 1)
	assert.Equal(t, price, priced[0].BasePriceAmount, "misafir sepeti şube fiyatını kullanır")

	// The guest menu must agree with the cart, or the diner is charged a
	// price they were never shown.
	menu, err := newPricingService().GetStorefrontMenu(ctx, f.tenantID, f.branchID)
	require.NoError(t, err)
	var shown int64
	for _, c := range menu {
		for _, p := range c.Products {
			if p.ID == f.productID {
				shown = p.PriceAmount
			}
		}
	}
	assert.Equal(t, price, shown, "menüde gösterilen fiyat sepetle aynı olmalı")

	// Closing the product removes it from the branch's menu entirely.
	_, err = newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchID, ProductID: f.productID, IsAvailable: false,
	})
	require.NoError(t, err)
	menu, err = newPricingService().GetStorefrontMenu(ctx, f.tenantID, f.branchID)
	require.NoError(t, err)
	for _, c := range menu {
		for _, p := range c.Products {
			assert.NotEqual(t, f.productID, p.ID, "kapalı ürün misafir menüsünde görünmemeli")
		}
	}
}

// TestBranchOverride_RLSHidesOtherTenants pins ADR-SEC-002 on the new table:
// a second tenant reusing the SAME branch and product ids (a plausible
// collision in a test, an impossible one to rule out in production) must see
// nothing and must be priced at its own tenant default.
func TestBranchOverride_RLSHidesOtherTenants(t *testing.T) {
	ctx := context.Background()
	f := seedOverrideFixture(t)
	other := seedOverrideFixture(t)
	price := kebapBranchPrice

	_, err := newBranchOverrideService().Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)

	// Same branch id, other tenant's context: the row is invisible.
	seen, err := newBranchOverrideService().ListByBranch(ctx, other.tenantID, f.branchB)
	require.NoError(t, err)
	assert.Empty(t, seen, "başka tenant'ın override'ı RLS ile görünmez")

	mine, err := newBranchOverrideService().ListByBranch(ctx, f.tenantID, f.branchB)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, f.kebap, mine[0].ProductID)

	assert.Equal(t, kebapTenantPrice, priceOf(t, other.tenantID, other.branchB, other.kebap))
}

// Delete returns the product to the tenant price. The override is gone, not
// merely blanked, so the branch stops appearing in the admin's override list.
func TestBranchOverride_DeleteRestoresTenantPrice(t *testing.T) {
	ctx := context.Background()
	f := seedOverrideFixture(t)
	svc := newBranchOverrideService()
	price := kebapBranchPrice

	_, err := svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)
	require.Equal(t, kebapBranchPrice, priceOf(t, f.tenantID, f.branchB, f.kebap))

	require.NoError(t, svc.Delete(ctx, f.tenantID, f.branchB, f.kebap))
	assert.Equal(t, kebapTenantPrice, priceOf(t, f.tenantID, f.branchB, f.kebap))

	rows, err := svc.ListByBranch(ctx, f.tenantID, f.branchB)
	require.NoError(t, err)
	assert.Empty(t, rows)

	// A second delete has nothing to remove.
	assert.ErrorIs(t, svc.Delete(ctx, f.tenantID, f.branchB, f.kebap), pub.ErrNotFound)
}

// Every write must leave an immutable event behind (ADR-DATA-001/002): a
// consumer caching effective prices learns about both a new branch price and
// its removal.
func TestBranchOverride_WritesOutboxEvent(t *testing.T) {
	ctx := context.Background()
	f := seedOverrideFixture(t)
	svc := newBranchOverrideService()
	price := kebapBranchPrice

	_, err := svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, f.tenantID, f.branchB, f.kebap))

	type event struct {
		eventType string
		payload   string
	}
	var events []event
	require.NoError(t, sharedPool.WithTenantReadTx(ctx, f.tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT event_type, payload::text FROM catalog_outbox
			WHERE aggregate_id = $1 ORDER BY created_at`, f.kebap)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e event
			if err := rows.Scan(&e.eventType, &e.payload); err != nil {
				return err
			}
			events = append(events, e)
		}
		return rows.Err()
	}))

	require.Len(t, events, 2, "upsert ve delete ayrı ayrı event yazar")
	for _, e := range events {
		// No module prefix: the dispatcher prepends it when building the
		// NATS subject (catalog.branch_override.changed.v1).
		assert.Equal(t, "branch_override.changed", e.eventType)
	}
	assert.Contains(t, events[0].payload, `"deleted": false`)
	assert.Contains(t, events[1].payload, `"deleted": true`)
}

func TestBranchOverride_RejectsUnknownProductAndNegativePrice(t *testing.T) {
	ctx := context.Background()
	f := seedOverrideFixture(t)
	svc := newBranchOverrideService()

	_, err := svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: uuid.New(), IsAvailable: true,
	})
	assert.ErrorIs(t, err, pub.ErrNotFound, "olmayan ürün 404, FK ihlali 500 değil")

	negative := int64(-1)
	_, err = svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &negative,
	})
	var invalid *pub.ValidationError
	assert.ErrorAs(t, err, &invalid)
}

// ---------------------------------------------------------------------------
// HTTP surface: OPA (layer 2) + the branch guard (layer 3) together.
// ---------------------------------------------------------------------------

func branchOverrideMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)

	h := cataloghttp.NewHandler(cataloghttp.Params{
		Products:        newProductService(),
		BranchOverrides: newBranchOverrideService(),
		Logger:          zap.NewNop(),
		Engine:          engine,
	})
	mux := chi.NewMux()
	h.RegisterRoutes(mux)
	return mux
}

func asPrincipal(req *http.Request, tenantID, branchID, roleID uuid.UUID) *http.Request {
	return req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{roleID},
	}))
}

func serve(mux *chi.Mux, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func putOverride(t *testing.T, mux *chi.Mux, tenantID, actorBranch, roleID, branchID, productID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/catalog/branches/"+branchID.String()+"/products/"+productID.String()+"/override",
		bytes.NewBufferString(body))
	return serve(mux, asPrincipal(req, tenantID, actorBranch, roleID))
}

// TestBranchOverrideHTTP_ManagerWritesCashierCannot is ADR-DATA-009 §6: the
// branch price is the chain owner's decision. A cashier holding
// catalog.product.read must not reach the write route at all.
func TestBranchOverrideHTTP_ManagerWritesCashierCannot(t *testing.T) {
	mux := branchOverrideMux(t)
	f := seedOverrideFixture(t)
	body := `{"is_available":true,"price_amount":37000}`

	managerRec := putOverride(t, mux, f.tenantID, uuid.Nil, catalogManagerRoleID, f.branchB, f.kebap, body)
	assert.Equal(t, http.StatusOK, managerRec.Code, managerRec.Body.String())

	// The cashier of THAT VERY BRANCH is still refused: it is the action that
	// is manager-only, not the branch.
	cashierRec := putOverride(t, mux, f.tenantID, f.branchB, catalogCashierRoleID, f.branchB, f.kebap, body)
	assert.Equal(t, http.StatusForbidden, cashierRec.Code, cashierRec.Body.String())
}

// A branch-bound principal may read its OWN branch's overrides and no other's
// (ADR-AUTH-001 layer 3) — OPA said "catalog.product.read is fine", the guard
// says "but not that branch".
func TestBranchOverrideHTTP_ReadIsBranchScoped(t *testing.T) {
	mux := branchOverrideMux(t)
	f := seedOverrideFixture(t)
	price := kebapBranchPrice
	_, err := newBranchOverrideService().Upsert(context.Background(), f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)

	own := serve(mux, asPrincipal(
		httptest.NewRequest(http.MethodGet, "/api/v1/catalog/branches/"+f.branchB.String()+"/product-overrides", nil),
		f.tenantID, f.branchB, catalogCashierRoleID))
	assert.Equal(t, http.StatusOK, own.Code, own.Body.String())

	foreign := serve(mux, asPrincipal(
		httptest.NewRequest(http.MethodGet, "/api/v1/catalog/branches/"+f.branchA.String()+"/product-overrides", nil),
		f.tenantID, f.branchB, catalogCashierRoleID))
	assert.Equal(t, http.StatusForbidden, foreign.Code, foreign.Body.String())
	assert.Contains(t, foreign.Body.String(), "branch_forbidden")
}

// The category listing is what the POS grid reads prices from, so it must
// answer with the branch's own numbers — and must not answer at all for a
// branch the caller does not work at.
func TestBranchOverrideHTTP_CategoryListingIsBranchEffective(t *testing.T) {
	mux := branchOverrideMux(t)
	f := seedOverrideFixture(t)
	ctx := context.Background()
	svc := newBranchOverrideService()
	price := kebapBranchPrice

	_, err := svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.kebap, IsAvailable: true, PriceAmount: &price,
	})
	require.NoError(t, err)
	_, err = svc.Upsert(ctx, f.tenantID, domain.BranchProductOverride{
		BranchID: f.branchB, ProductID: f.ayran, IsAvailable: false,
	})
	require.NoError(t, err)

	type listed struct {
		ID                    uuid.UUID `json:"id"`
		PriceAmount           int64     `json:"price_amount"`
		BranchPriceOverridden bool      `json:"branch_price_overridden"`
	}
	read := func(actorBranch, roleID uuid.UUID, query string) (*httptest.ResponseRecorder, []listed) {
		rec := serve(mux, asPrincipal(
			httptest.NewRequest(http.MethodGet, "/api/v1/catalog/categories/"+f.category.String()+"/products"+query, nil),
			f.tenantID, actorBranch, roleID))
		var out []listed
		if rec.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		}
		return rec, out
	}

	_, atB := read(f.branchB, catalogCashierRoleID, "?branch_id="+f.branchB.String())
	require.Len(t, atB, 1, "şubede kapalı ürün listede yok")
	assert.Equal(t, f.kebap, atB[0].ID)
	assert.Equal(t, kebapBranchPrice, atB[0].PriceAmount)
	assert.True(t, atB[0].BranchPriceOverridden)

	_, atA := read(f.branchA, catalogCashierRoleID, "?branch_id="+f.branchA.String())
	require.Len(t, atA, 2, "override'sız şube tüm ürünleri görür")
	for _, p := range atA {
		assert.False(t, p.BranchPriceOverridden)
	}

	// No branch_id at all: the pre-ADR-DATA-009 answer, unchanged.
	_, tenantWide := read(uuid.Nil, catalogManagerRoleID, "")
	require.Len(t, tenantWide, 2)
	for _, p := range tenantWide {
		assert.False(t, p.BranchPriceOverridden)
		if p.ID == f.kebap {
			assert.Equal(t, kebapTenantPrice, p.PriceAmount)
		}
	}

	foreign, _ := read(f.branchB, catalogCashierRoleID, "?branch_id="+f.branchA.String())
	assert.Equal(t, http.StatusForbidden, foreign.Code, foreign.Body.String())

	// The uncategorised listing is the second endpoint that takes branch_id.
	// It shares respondBranchProducts but reaches it down its own path, so it
	// is asserted rather than assumed.
	flat := serve(mux, asPrincipal(
		httptest.NewRequest(http.MethodGet, "/api/v1/catalog/products?branch_id="+f.branchB.String(), nil),
		f.tenantID, f.branchB, catalogCashierRoleID))
	require.Equal(t, http.StatusOK, flat.Code, flat.Body.String())
	var flatRows []listed
	require.NoError(t, json.Unmarshal(flat.Body.Bytes(), &flatRows))
	byID := make(map[uuid.UUID]listed, len(flatRows))
	for _, p := range flatRows {
		byID[p.ID] = p
	}
	assert.Equal(t, kebapBranchPrice, byID[f.kebap].PriceAmount)
	assert.True(t, byID[f.kebap].BranchPriceOverridden)
	assert.NotContains(t, byID, f.ayran, "şubede kapalı ürün tenant listesinde de görünmemeli")

	flatForeign := serve(mux, asPrincipal(
		httptest.NewRequest(http.MethodGet, "/api/v1/catalog/products?branch_id="+f.branchA.String(), nil),
		f.tenantID, f.branchB, catalogCashierRoleID))
	assert.Equal(t, http.StatusForbidden, flatForeign.Code, flatForeign.Body.String())
}
